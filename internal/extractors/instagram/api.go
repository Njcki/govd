package instagram

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
	"github.com/govdbot/govd/internal/util"
)

const (
	mediaInfoEndpoint = "https://www.instagram.com/api/v1/media/%s/info/"
	webAppID          = "936619743392459"
)

// Instagram media_type values from /api/v1/media/{id}/info/
const (
	mediaTypePhoto    = 1
	mediaTypeVideo    = 2
	mediaTypeCarousel = 8
)

// ShortcodeToMediaID converts an Instagram shortcode to a numeric media ID.
func ShortcodeToMediaID(shortcode string) (string, error) {
	if shortcode == "" || len(shortcode) > 11 {
		return "", fmt.Errorf("invalid shortcode: %q", shortcode)
	}
	padded := strings.Repeat("A", 12-len(shortcode)) + shortcode
	decoded, err := base64.RawURLEncoding.DecodeString(padded)
	if err != nil {
		return "", fmt.Errorf("failed to decode shortcode: %w", err)
	}
	var mediaID uint64
	for _, b := range decoded {
		mediaID = (mediaID << 8) | uint64(b)
	}
	return strconv.FormatUint(mediaID, 10), nil
}

// GetMediaInfoMedia fetches post media via Instagram's authenticated
// /api/v1/media/{id}/info/ endpoint. Works for photos, videos, and mixed carousels.
func GetMediaInfoMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	mediaID, err := ShortcodeToMediaID(ctx.ContentID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert shortcode: %w", err)
	}

	apiURL := fmt.Sprintf(mediaInfoEndpoint, mediaID)
	headers := map[string]string{
		"Accept":             "*/*",
		"Accept-Language":    "en-US,en;q=0.9",
		"x-ig-app-id":        webAppID,
		"x-asbd-id":          "129477",
		"x-requested-with":   "XMLHttpRequest",
		"Sec-Fetch-Dest":     "empty",
		"Sec-Fetch-Mode":     "cors",
		"Sec-Fetch-Site":     "same-origin",
		"Referer":            "https://www.instagram.com/p/" + ctx.ContentID + "/",
		"User-Agent":         "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	}

	csrf := ""
	for _, c := range util.GetExtractorCookies("instagram") {
		if c != nil && c.Name == "csrftoken" && c.Value != "" {
			csrf = c.Value
			break
		}
	}
	if csrf != "" {
		headers["X-CSRFToken"] = csrf
	}

	resp, err := ctx.Fetch(
		http.MethodGet,
		apiURL,
		&networking.RequestParams{
			Headers: headers,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to send media info request: %w", err)
	}
	defer resp.Body.Close()

	logger.WriteFile("ig_media_info_response", resp)

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("media info unauthorized: %s", resp.Status)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("media info bad status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read media info body: %w", err)
	}

	var response MediaInfoResponse
	if err := sonic.ConfigFastest.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse media info: %w", err)
	}
	if response.Status != "" && response.Status != "ok" {
		return nil, fmt.Errorf("media info status: %s", response.Status)
	}
	if len(response.Items) == 0 {
		return nil, fmt.Errorf("media info returned no items")
	}

	return ParseMediaInfoItem(ctx, response.Items[0])
}

// ParseMediaInfoItem maps an Instagram media info item into govd Media.
// media_type 1 = photo, 2 = video, 8 = carousel (mixed photo/video supported).
func ParseMediaInfoItem(ctx *models.ExtractorContext, item *MediaInfoItem) (*models.Media, error) {
	if item == nil {
		return nil, fmt.Errorf("media info item is nil")
	}

	media := ctx.NewMedia()
	if item.Caption != nil && item.Caption.Text != "" {
		media.SetCaption(item.Caption.Text)
	}

	switch item.MediaType {
	case mediaTypePhoto, mediaTypeVideo:
		if err := addMediaInfoFormats(media, item); err != nil {
			return nil, err
		}
	case mediaTypeCarousel:
		if len(item.CarouselMedia) == 0 {
			return nil, fmt.Errorf("carousel has no media items")
		}
		for _, child := range item.CarouselMedia {
			if child == nil {
				continue
			}
			if err := addMediaInfoFormats(media, child); err != nil {
				// Skip broken children but keep other carousel items.
				ctx.Warnf("skipping carousel item: %v", err)
				continue
			}
		}
	default:
		return nil, fmt.Errorf("unsupported media_type: %d", item.MediaType)
	}

	if len(media.Items) == 0 {
		return nil, fmt.Errorf("no extractable media found in media info response")
	}
	return media, nil
}

func addMediaInfoFormats(media *models.Media, item *MediaInfoItem) error {
	format, err := mediaInfoFormatFromItem(item)
	if err != nil {
		return err
	}
	itemOut := media.NewItem()
	itemOut.AddFormats(format)
	return nil
}

func mediaInfoFormatFromItem(item *MediaInfoItem) (*models.MediaFormat, error) {
	switch item.MediaType {
	case mediaTypePhoto:
		if item.ImageVersions == nil || len(item.ImageVersions.Candidates) == 0 {
			return nil, fmt.Errorf("photo has no image_versions2 candidates")
		}
		best := GetBestCandidate(item.ImageVersions.Candidates)
		if best == nil || best.URL == "" {
			return nil, fmt.Errorf("photo candidate url is empty")
		}
		return &models.MediaFormat{
			FormatID: "image",
			Type:     database.MediaTypePhoto,
			URL:      []string{best.URL},
			Width:    int32(best.Width),
			Height:   int32(best.Height),
		}, nil
	case mediaTypeVideo:
		if len(item.VideoVersions) == 0 {
			return nil, fmt.Errorf("video has no video_versions")
		}
		best := GetBestVideoVersion(item.VideoVersions)
		if best == nil || best.URL == "" {
			return nil, fmt.Errorf("video version url is empty")
		}
		thumbURL := ""
		if item.ImageVersions != nil {
			if thumb := GetBestCandidate(item.ImageVersions.Candidates); thumb != nil {
				thumbURL = thumb.URL
			}
		}
		format := &models.MediaFormat{
			FormatID:   "video",
			Type:       database.MediaTypeVideo,
			VideoCodec: database.MediaCodecAvc,
			AudioCodec: database.MediaCodecAac,
			URL:        []string{best.URL},
			Width:      int32(best.Width),
			Height:     int32(best.Height),
		}
		if thumbURL != "" {
			format.ThumbnailURL = []string{thumbURL}
		}
		if item.VideoDuration > 0 {
			format.Duration = int32(item.VideoDuration)
		}
		return format, nil
	default:
		// Nested unexpected type inside carousel: try photo then video.
		if item.ImageVersions != nil && len(item.ImageVersions.Candidates) > 0 && len(item.VideoVersions) == 0 {
			best := GetBestCandidate(item.ImageVersions.Candidates)
			if best != nil && best.URL != "" {
				return &models.MediaFormat{
					FormatID: "image",
					Type:     database.MediaTypePhoto,
					URL:      []string{best.URL},
					Width:    int32(best.Width),
					Height:   int32(best.Height),
				}, nil
			}
		}
		if len(item.VideoVersions) > 0 {
			best := GetBestVideoVersion(item.VideoVersions)
			if best != nil && best.URL != "" {
				return &models.MediaFormat{
					FormatID:   "video",
					Type:       database.MediaTypeVideo,
					VideoCodec: database.MediaCodecAvc,
					AudioCodec: database.MediaCodecAac,
					URL:        []string{best.URL},
					Width:      int32(best.Width),
					Height:     int32(best.Height),
				}, nil
			}
		}
		return nil, fmt.Errorf("unsupported carousel child media_type: %d", item.MediaType)
	}
}
