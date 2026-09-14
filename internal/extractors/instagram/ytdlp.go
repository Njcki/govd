package instagram

import (
	"fmt"
	"io"
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

const instagramCookieFile = "private/cookies/instagram.txt"

// GetYTDLPMedia downloads Instagram media via yt-dlp using session cookies.
// Skips gracefully when yt-dlp or cookies are unavailable.
func GetYTDLPMedia(ctx *models.ExtractorContext) (*models.Media, error) {
	ytdlpPath, err := exec.LookPath("yt-dlp")
	if err != nil {
		return nil, fmt.Errorf("yt-dlp not available")
	}

	if _, err := os.Stat(instagramCookieFile); err != nil {
		return nil, fmt.Errorf("instagram cookie file not found")
	}

	// yt-dlp may rewrite --cookies files and drop sessionid; always use a temp copy.
	cookiesCopy, err := os.CreateTemp("", "ig-cookies-*.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp cookie file: %w", err)
	}
	cookiesCopyPath := cookiesCopy.Name()
	src, err := os.Open(instagramCookieFile)
	if err != nil {
		cookiesCopy.Close()
		_ = os.Remove(cookiesCopyPath)
		return nil, fmt.Errorf("failed to open cookie file: %w", err)
	}
	_, copyErr := io.Copy(cookiesCopy, src)
	src.Close()
	closeErr := cookiesCopy.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(cookiesCopyPath)
		if copyErr != nil {
			return nil, fmt.Errorf("failed to copy cookie file: %w", copyErr)
		}
		return nil, fmt.Errorf("failed to close temp cookie file: %w", closeErr)
	}
	defer os.Remove(cookiesCopyPath)

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
		fmt.Sprintf("ig-ytdlp-%s-%s.%%(ext)s", ctx.ContentID, uid),
	)
	globPattern := filepath.Join(
		downloadsDir,
		fmt.Sprintf("ig-ytdlp-%s-%s.*", ctx.ContentID, uid),
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
		contentURL = "https://www.instagram.com/reel/" + ctx.ContentID + "/"
	}

	caption := ""
	infoCmd := exec.CommandContext(
		ctx.Context,
		ytdlpPath,
		"--no-playlist",
		"--no-warnings",
		"--user-agent", networking.DefaultUserAgent,
		"--skip-download",
		"--cookies", cookiesCopyPath,
		"--print", "%(description)s",
		contentURL,
	)
	if infoOut, infoErr := infoCmd.CombinedOutput(); infoErr == nil {
		caption = strings.TrimSpace(string(infoOut))
		if len(caption) > 600 {
			caption = caption[:600] + "..."
		}
	}

	cmd := exec.CommandContext(
		ctx.Context,
		ytdlpPath,
		"--no-playlist",
		"--no-warnings",
		"--no-progress",
		"--user-agent", networking.DefaultUserAgent,
		"--cookies", cookiesCopyPath,
		"-f", "bv*+ba/b",
		"--merge-output-format", "mp4",
		"-o", outTemplate,
		contentURL,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		cleanup()
		msg := strings.TrimSpace(string(output))
		if len(msg) > 300 {
			msg = msg[:300]
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
		FormatID:   "video",
		Type:       database.MediaTypeVideo,
		VideoCodec: database.MediaCodecAvc,
		AudioCodec: database.MediaCodecAac,
		URL:        []string{"file://" + filePath},
	})

	ctx.Debugf("yt-dlp downloaded instagram media to local file")
	return media, nil
}
