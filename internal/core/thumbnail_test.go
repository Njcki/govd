package core

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
)

func newThumbTestCtx(t *testing.T) *models.ExtractorContext {
	t.Helper()
	logger.Init()
	return &models.ExtractorContext{
		ContentURL:   "https://example.com/test",
		Extractor:    &models.Extractor{ID: "test"},
		HTTPClient:   networking.NewHTTPClient(nil),
		FilesTracker: models.NewFilesTracker(),
		Context:      context.Background(),
	}
}

func forbiddenServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// a failing thumbnail download must never fail the whole download
func TestGetThumbnailDownloadFailureIsNotFatal(t *testing.T) {
	ctx := newThumbTestCtx(t)
	srv := forbiddenServer(t)

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(videoPath, []byte("not a video"), 0o644); err != nil {
		t.Fatal(err)
	}
	format := &models.MediaFormat{
		Type:         database.MediaTypeVideo,
		ThumbnailURL: []string{srv.URL + "/thumb.jpg"},
	}
	if got := getThumbnail(ctx, format, videoPath); got != "" {
		t.Fatalf("expected empty thumbnail path, got %q", got)
	}
}

// with a real video, a failed thumbnail download falls back to ffmpeg
func TestGetThumbnailFallsBackToFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	ctx := newThumbTestCtx(t)
	srv := forbiddenServer(t)

	dir := t.TempDir()
	videoPath := filepath.Join(dir, "video.mp4")
	cmd := exec.Command("ffmpeg", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "color=c=red:s=64x64:d=1",
		"-c:v", "mjpeg", "-f", "mp4", videoPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot generate test video: %v: %s", err, out)
	}
	format := &models.MediaFormat{
		Type:         database.MediaTypeVideo,
		ThumbnailURL: []string{srv.URL + "/thumb.jpg"},
	}
	got := getThumbnail(ctx, format, videoPath)
	if got == "" {
		t.Fatal("expected ffmpeg fallback thumbnail")
	}
	if st, err := os.Stat(got); err != nil || st.Size() == 0 {
		t.Fatalf("thumbnail not written: %v", err)
	}
}
