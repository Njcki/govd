package core

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/govdbot/govd/internal/config"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
)

// formats are only accepted if their (estimated) size is within
// this fraction of MAX_FILE_SIZE, to leave room for remux overhead
// and estimate errors
const sizeSafetyMargin = 0.95

const sizeProbeTimeout = 15 * time.Second

func sizeLimit(maxFileSize int64) int64 {
	return int64(float64(maxFileSize) * sizeSafetyMargin)
}

// pickFormatWithinSize returns the first candidate (candidates are
// ordered by preference, best first) whose size fits within the
// limit. sizeOf returns the best known size of a format, 0 if
// unknown; unknown sizes are treated as fitting, matching the
// previous behavior. if the candidate has no audio and audio is
// non-nil (it will be merged), the audio size is added.
// if nothing fits, the first candidate is returned with downgraded
// set to false, so the existing file-too-large handling applies.
func pickFormatWithinSize(
	candidates []*models.MediaFormat,
	audio *models.MediaFormat,
	limit int64,
	sizeOf func(*models.MediaFormat) int64,
) (chosen *models.MediaFormat, size int64, downgraded bool) {
	if len(candidates) == 0 {
		return nil, 0, false
	}
	if limit <= 0 {
		return candidates[0], 0, false
	}

	var firstSize int64
	for i, candidate := range candidates {
		s := sizeOf(candidate)
		if s > 0 && candidate.AudioCodec == "" && audio != nil {
			if a := sizeOf(audio); a > 0 {
				s += a
			}
		}
		if i == 0 {
			firstSize = s
		}
		if s == 0 || s <= limit {
			return candidate, s, i > 0
		}
	}
	return candidates[0], firstSize, false
}

// selectVideoFormat picks the preferred video format respecting the
// per-chat max height, downgrading to the best format whose size fits
// MAX_FILE_SIZE when the preferred one would be too large.
// returns nil if the item has no video formats.
func selectVideoFormat(
	ctx *models.ExtractorContext,
	item *models.MediaItem,
	maxHeight int32,
) *models.MediaFormat {
	candidates := item.GetVideoFormatCandidates(maxHeight)
	if len(candidates) == 0 {
		return nil
	}
	if len(candidates) == 1 || config.Env.MaxFileSize <= 0 {
		return candidates[0]
	}

	limit := sizeLimit(config.Env.MaxFileSize)
	sizeOf := func(f *models.MediaFormat) int64 {
		if f.FileSize > 0 {
			return f.FileSize
		}
		est := f.EstimatedSize()
		// bitrate estimates can be far off (X reports peak bitrate),
		// so before rejecting a format, ask the server for the real size
		if est > limit {
			if real := probeFormatSize(ctx, f); real > 0 {
				f.FileSize = real
				return real
			}
		}
		return est
	}

	chosen, size, downgraded := pickFormatWithinSize(
		candidates, item.GetDefaultAudioFormat(), limit, sizeOf,
	)
	if downgraded {
		ctx.Infof(
			"preferred format %s exceeds max file size, downgraded to %s (est. %d MB, limit %d MB)",
			candidates[0].FormatID, chosen.FormatID,
			size/(1024*1024), limit/(1024*1024),
		)
	}
	return chosen
}

// probeFormatSize returns the size reported by the server for a
// single-URL format (HEAD, falling back to a 1-byte ranged GET),
// or 0 if unknown.
func probeFormatSize(ctx *models.ExtractorContext, f *models.MediaFormat) int64 {
	if ctx == nil || ctx.HTTPClient == nil || len(f.URL) == 0 || len(f.Segments) > 0 {
		return 0
	}
	if f.Type != database.MediaTypeVideo && f.Type != database.MediaTypeAudio {
		return 0
	}
	url := f.URL[0]
	if !strings.HasPrefix(url, "http") || strings.Contains(url, ".m3u8") || strings.Contains(url, ".mpd") {
		return 0
	}

	parent := context.Background()
	if ctx.Context != nil {
		parent = ctx.Context
	}
	reqCtx, cancel := context.WithTimeout(parent, sizeProbeTimeout)
	defer cancel()

	client := ctx.HTTPClient.AsDownloadClient()
	var headers map[string]string
	var cookies []*http.Cookie
	if f.DownloadSettings != nil {
		headers = f.DownloadSettings.Headers
		cookies = f.DownloadSettings.Cookies
	}

	resp, err := client.FetchWithContext(reqCtx, http.MethodHead, url, &networking.RequestParams{
		Headers: headers,
		Cookies: cookies,
	})
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && resp.ContentLength > 0 {
			return resp.ContentLength
		}
	}

	rangeHeaders := map[string]string{"Range": "bytes=0-0"}
	for k, v := range headers {
		rangeHeaders[k] = v
	}
	resp, err = client.FetchWithContext(reqCtx, http.MethodGet, url, &networking.RequestParams{
		Headers: rangeHeaders,
		Cookies: cookies,
	})
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPartialContent {
		return parseContentRangeTotal(resp.Header.Get("Content-Range"))
	}
	return 0
}

// parses "bytes 0-0/12345" -> 12345
func parseContentRangeTotal(cr string) int64 {
	idx := strings.LastIndex(cr, "/")
	if idx < 0 || idx == len(cr)-1 {
		return 0
	}
	total, err := strconv.ParseInt(strings.TrimSpace(cr[idx+1:]), 10, 64)
	if err != nil || total <= 0 {
		return 0
	}
	return total
}
