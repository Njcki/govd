package youtube

import (
	"fmt"
	"net/http"
	"regexp"

	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/util"

	"github.com/bytedance/sonic"
)

var Extractor = &models.Extractor{
	ID:          "youtube",
	DisplayName: "YouTube",

	URLPattern: regexp.MustCompile(`(?:https?:)?(?:\/\/)?(?:(?:www|m)\.)?(?:youtube(?:-nocookie)?\.com\/(?:(?:watch\?(?:.*&)?v=)|(?:embed\/)|(?:v\/)|(?:shorts\/))|youtu\.be\/)(?P<id>[\w-]{11})(?:[?&].*)?`),
	Host: []string{
		"youtube",
		"youtu",
		"youtube-nocookie",
	},

	GetFunc: func(ctx *models.ExtractorContext) (*models.ExtractorResponse, error) {
		video, errInv := GetVideoFromInv(ctx)
		if errInv == nil && video != nil {
			return &models.ExtractorResponse{Media: video}, nil
		}
		if errInv != nil {
			ctx.Debugf("invidious path failed: %v", errInv)
		}
		video, errYT := GetYTDLPMedia(ctx)
		if errYT == nil {
			return &models.ExtractorResponse{Media: video}, nil
		}
		if errInv != nil {
			return nil, fmt.Errorf("youtube failed: invidious: %w; yt-dlp: %v", errInv, errYT)
		}
		return nil, errYT
	},
}

func GetVideoFromInv(ctx *models.ExtractorContext) (*models.Media, error) {
	if ctx.Config == nil || len(ctx.Config.Instance) == 0 {
		return nil, fmt.Errorf("youtube not configured")
	}
	var lastErr error
	for i := range ctx.Config.Instance {
		instance, err := GetInvInstance(ctx, i)
		if err != nil {
			lastErr = err
			continue
		}
		media, err := GetFromInstance(ctx, instance)
		if err == nil {
			return media, nil
		}
		lastErr = err
		ctx.Debugf("invidious instance %s failed: %v", instance, err)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no working invidious instance")
	}
	return nil, lastErr
}

func GetFromInstance(ctx *models.ExtractorContext, instance string) (*models.Media, error) {
	videoID := ctx.ContentID
	reqURL := instance +
		invEndpoint +
		videoID +
		"?local=true" // proxied CDN

	ctx.Debugf("proxied invidious api: %s", reqURL)

	resp, err := ctx.Fetch(
		http.MethodGet,
		reqURL, nil,
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	logger.WriteFile("inv_youtube_response", resp)

	var data *InvResponse
	decoder := sonic.ConfigFastest.NewDecoder(resp.Body)
	err = decoder.Decode(&data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	switch data.Error {
	case "This video may be inappropriate for some users.":
		return nil, util.ErrAgeRestricted
	default:
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("bad response: %s", resp.Status)
		}
	}

	formats := ParseInvFormats(data, instance)
	if len(formats) == 0 {
		return nil, fmt.Errorf("no formats found")
	}

	media := ctx.NewMedia()
	item := media.NewItem()
	item.AddFormats(formats...)

	return media, nil
}
