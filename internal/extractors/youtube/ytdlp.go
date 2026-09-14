package youtube

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/govdbot/govd/internal/config"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
)

const youtubeCookieFile = "private/cookies/youtube.txt"

// GetYTDLPMedia downloads a YouTube video via yt-dlp to a local file.
// Optional cookies: private/cookies/youtube.txt (Netscape).
func GetYTDLPMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	ytdlpPath, err := exec.LookPath("yt-dlp")
	if err != nil {
		return nil, fmt.Errorf("yt-dlp not available")
	}

	downloadsDir := config.Env.DownloadsDirectory
	if downloadsDir == "" {
		downloadsDir = "downloads"
	}
	if err := os.MkdirAll(downloadsDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create downloads dir: %w", err)
	}

	uid := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	outTemplate := filepath.Join(
		downloadsDir,
		fmt.Sprintf("yt-ytdlp-%s-%s.%%(ext)s", ctx.ContentID, uid),
	)
	globPattern := filepath.Join(
		downloadsDir,
		fmt.Sprintf("yt-ytdlp-%s-%s.*", ctx.ContentID, uid),
	)

	cleanup := func() {
		matches, _ := filepath.Glob(globPattern)
		for _, m := range matches {
			_ = os.Remove(m)
		}
		partMatches, _ := filepath.Glob(globPattern + ".part")
		for _, m := range partMatches {
			_ = os.Remove(m)
		}
	}

	contentURL := ctx.ContentURL
	if contentURL == "" {
		contentURL = "https://www.youtube.com/watch?v=" + ctx.ContentID
	}

	maxHeight := int32(0)
	if ctx.Chat != nil {
		maxHeight = ctx.Chat.MaxVideoHeight
	}

	// Prefer AVC+AAC for Telegram; honor max video quality when set.
	format := "bv*[vcodec*=avc1]+ba[acodec*=mp4a]/b[ext=mp4]/b"
	if maxHeight > 0 {
		format = fmt.Sprintf(
			"bv*[height<=%d][vcodec*=avc1]+ba[acodec*=mp4a]/b[height<=%d][ext=mp4]/b",
			maxHeight, maxHeight,
		)
	}

	args := []string{
		"--no-playlist",
		"--no-warnings",
		"--no-progress",
		"--user-agent", networking.DefaultUserAgent,
		"-f", format,
		"--merge-output-format", "mp4",
		"-o", outTemplate,
	}
	if _, err := os.Stat(youtubeCookieFile); err == nil {
		args = append(args, "--cookies", youtubeCookieFile)
	}
	args = append(args, contentURL)

	caption := ""
	infoArgs := []string{
		"--no-playlist",
		"--no-warnings",
		"--user-agent", networking.DefaultUserAgent,
		"--skip-download",
		"--print", "%(title)s",
	}
	if _, err := os.Stat(youtubeCookieFile); err == nil {
		infoArgs = append(infoArgs, "--cookies", youtubeCookieFile)
	}
	infoArgs = append(infoArgs, contentURL)
	infoCmd := exec.CommandContext(ctx.Context, ytdlpPath, infoArgs...)
	if infoOut, infoErr := infoCmd.CombinedOutput(); infoErr == nil {
		caption = strings.TrimSpace(string(infoOut))
		if len(caption) > 600 {
			caption = caption[:600] + "..."
		}
	}

	cmd := exec.CommandContext(ctx.Context, ytdlpPath, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		msg := strings.TrimSpace(string(output))
		if len(msg) > 400 {
			msg = msg[:400]
		}
		return nil, fmt.Errorf("yt-dlp failed: %w (%s)", err, msg)
	}

	matches, err := filepath.Glob(globPattern)
	if err != nil || len(matches) == 0 {
		cleanup()
		return nil, fmt.Errorf("yt-dlp produced no output file")
	}

	filePath := matches[0]
	for _, m := range matches {
		if strings.HasSuffix(strings.ToLower(m), ".mp4") {
			filePath = m
			break
		}
	}

	ctx.FilesTracker.Add(filePath)
	for _, m := range matches {
		if m != filePath {
			ctx.FilesTracker.Add(m)
		}
	}

	media := ctx.NewMedia()
	media.Caption = caption
	item := media.NewItem()
	item.AddFormats(&models.MediaFormat{
		FormatID:   "ytdlp",
		Type:       database.MediaTypeVideo,
		VideoCodec: database.MediaCodecAvc,
		AudioCodec: database.MediaCodecAac,
		Height:     maxHeight, // informational; 0 means Best
		URL:        []string{"file://" + filePath},
	})

	ctx.Debugf("yt-dlp downloaded youtube media to local file")
	return media, nil
}
