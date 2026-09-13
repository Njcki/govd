package instagram

import (
	"fmt"
	"io"
	"maps"
	"net/http"
	"regexp"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
	"github.com/govdbot/govd/internal/util"
)

var instagramHost = []string{"instagram", "ddinstagram"}

var Extractor = &models.Extractor{
	ID:          "instagram",
	DisplayName: "Instagram",

	URLPattern: regexp.MustCompile(`https:\/\/(www\.)?(?:dd)?instagram\.com\/(reels?|p|tv)\/(?P<id>[a-zA-Z0-9_-]+)`),
	Host:       instagramHost,
	Redirect:   false,

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		if !HasSessionIDCookie() {
			return nil, util.ErrInstagramCookies
		}
		// method 1: get media from GQL web API (uses real session cookies)
		media, err1 := GetGQLMedia(ctx)
		if err1 == nil && mediaHasItems(media) {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		if err1 == nil {
			err1 = fmt.Errorf("graphql returned no media items")
		}
		// method 2: authenticated /api/v1/media/{id}/info/ (photos + mixed carousels)
		media, err2 := GetMediaInfoMedia(ctx)
		if err2 == nil && mediaHasItems(media) {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		if err2 == nil {
			err2 = fmt.Errorf("media info returned no media items")
		}
		// method 3: yt-dlp fallback (videos; photos often unsupported)
		media, err3 := GetYTDLPMedia(ctx)
		if err3 == nil && mediaHasItems(media) {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		if err3 == nil {
			err3 = fmt.Errorf("yt-dlp returned no media items")
		}
		// method 4: get media from embed page
		media, err4 := GetEmbedMedia(ctx)
		if err4 == nil && mediaHasItems(media) {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		if err4 == nil {
			err4 = fmt.Errorf("embed returned no media items")
		}
		// method 5: get media from 3rd party service (unlikely)
		media, err5 := GetIGramPost(ctx)
		if err5 == nil && mediaHasItems(media) {
			return &models.ExtractorResponse{
				Media: media,
			}, nil
		}
		if err5 == nil {
			err5 = fmt.Errorf("igram returned no media items")
		}
		if isInstagramAuthFailure(err1) || isInstagramAuthFailure(err2) {
			return nil, util.ErrInstagramCookies
		}
		return nil, fmt.Errorf(
			"all methods failed (no extractable media): gql=%v; media_info=%v; yt-dlp=%v; embed=%v; igram=%v",
			err1, err2, err3, err4, err5,
		)
	},
}

var StoriesExtractor = &models.Extractor{
	ID:          "instagram",
	DisplayName: "Instagram Stories",

	URLPattern: regexp.MustCompile(`https:\/\/(www\.)?(?:dd)?instagram\.com\/stories\/[a-zA-Z0-9._]+\/(?P<id>\d+)`),
	Host:       instagramHost,
	Hidden:     true,

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		media, err := GetIGramStory(ctx)
		return &models.ExtractorResponse{
			Media: media,
		}, err
	},
}

var ShareURLExtractor = &models.Extractor{
	ID:          "instagram",
	DisplayName: "Instagram (Share)",

	URLPattern: regexp.MustCompile(`https?:\/\/(www\.)?(?:dd)?instagram\.com\/share\/((reels?|video|s|p)\/)?(?P<id>[^\/\?]+)`),
	Host:       instagramHost,

	Redirect: true,

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		redirectURL, err := ctx.FetchLocation(ctx.ContentURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get url location: %w", err)
		}
		return &models.ExtractorResponse{URL: redirectURL}, nil
	},
}

func GetGQLMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	graphData, err := GetGQLData(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get graph data: %w", err)
	}
	return ParseGQLMedia(ctx, graphData.ShortcodeMedia)
}

func GetEmbedMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	embedURL := fmt.Sprintf(
		"https://www.instagram.com/p/%s/embed/captioned",
		ctx.ContentID,
	)
	resp, err := ctx.Fetch(
		http.MethodGet,
		embedURL,
		&networking.RequestParams{
			Headers: webHeaders,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_embed_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get embed page: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	graphData, err := ParseEmbedGQL(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse embed page: %w", err)
	}
	return ParseGQLMedia(ctx, graphData)
}

func GetIGramPost(ctx *models.ExtractorContext) (*models.Media, error) {
	details, err := GetPostFromIGram(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get post: %w", err)
	}

	media := ctx.NewMedia()
	for _, obj := range details.Items {
		item := media.NewItem()
		if len(obj.URL) == 0 {
			return nil, fmt.Errorf("no media url found")
		}
		urlObj := obj.URL[0]
		contentURL, err := GetCDNURL(urlObj.URL)
		if err != nil {
			return nil, err
		}
		thumbnailURL, err := GetCDNURL(obj.Thumb)
		if err != nil {
			return nil, err
		}
		fileExt := urlObj.Ext
		formatID := urlObj.Type
		switch fileExt {
		case "mp4":
			item.AddFormats(&models.MediaFormat{
				FormatID:     formatID,
				Type:         database.MediaTypeVideo,
				URL:          []string{contentURL},
				VideoCodec:   database.MediaCodecAvc,
				AudioCodec:   database.MediaCodecAac,
				ThumbnailURL: []string{thumbnailURL},
			},
			)
		case "jpg", "png", "webp", "heic", "jpeg":
			item.AddFormats(&models.MediaFormat{
				Type:     database.MediaTypePhoto,
				FormatID: formatID,
				URL:      []string{contentURL},
			})
		default:
			return nil, fmt.Errorf("unknown format: %s", fileExt)
		}
	}

	if len(media.Items) == 0 {
		return nil, fmt.Errorf("no media found")
	}

	return media, nil
}

func GetIGramStory(ctx *models.ExtractorContext) (*models.Media, error) {
	details, err := GetStoryFromIGram(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get story: %w", err)
	}

	if len(details.Result) == 0 {
		return nil, util.ErrUnavailable
	}
	result := details.Result[0]
	isVideo := len(result.VideoVersions) > 0

	media := ctx.NewMedia()
	item := media.NewItem()
	if isVideo {
		video := GetBestVideoVersion(result.VideoVersions)
		item.AddFormats(&models.MediaFormat{
			FormatID:   "video",
			Type:       database.MediaTypeVideo,
			URL:        []string{video.URL},
			VideoCodec: database.MediaCodecAvc,
			AudioCodec: database.MediaCodecAac,
		})
	} else {
		image := GetBestCandidate(result.ImageVersions.Candidates)
		item.AddFormats(&models.MediaFormat{
			Type:     database.MediaTypePhoto,
			FormatID: "photo",
			URL:      []string{image.URL},
		})
	}

	if len(media.Items) == 0 {
		return nil, fmt.Errorf("no media found")
	}

	return media, nil
}

func GetPostFromIGram(ctx *models.ExtractorContext) (*IGramResponse, error) {
	contentURL := "https://www.instagram.com/p/" + ctx.ContentID + "/"
	apiURL := fmt.Sprintf("https://%s/api/convert", igramHostname)
	payload, err := IGramBodyFromURL(contentURL)
	if err != nil {
		return nil, fmt.Errorf("failed to build signed payload: %w", err)
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	maps.Copy(headers, igramHeaders)

	resp, err := ctx.Fetch(
		http.MethodPost,
		apiURL,
		&networking.RequestParams{
			Body:    payload,
			Headers: headers,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_3party_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get response: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	response, err := ParseIGramResponse(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}
	return response, nil
}

func GetStoryFromIGram(ctx *models.ExtractorContext) (*IGramStoryResponse, error) {
	apiURL := fmt.Sprintf("https://%s/api/v1/instagram/story", igramHostname)
	payload, err := IGramBodyFromParams(map[string]string{
		"url": ctx.ContentURL,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to build signed payload: %w", err)
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	maps.Copy(headers, igramHeaders)

	resp, err := ctx.Fetch(
		http.MethodPost,
		apiURL,
		&networking.RequestParams{
			Body:    payload,
			Headers: headers,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_story_3party_response", resp)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get response: %s", resp.Status)
	}

	var story IGramStoryResponse
	decoder := sonic.ConfigFastest.NewDecoder(resp.Body)
	err = decoder.Decode(&story)
	if err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &story, nil
}


func mediaHasItems(media *models.Media) bool {
	return media != nil && len(media.Items) > 0
}

func HasSessionIDCookie() bool {
	for _, c := range util.GetExtractorCookies("instagram") {
		if c != nil && c.Name == "sessionid" && c.Value != "" {
			return true
		}
	}
	return false
}

func isInstagramAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") ||
		strings.Contains(msg, "403") ||
		strings.Contains(msg, "unauthorized") ||
		strings.Contains(msg, "forbidden") ||
		strings.Contains(msg, "login_required")
}
