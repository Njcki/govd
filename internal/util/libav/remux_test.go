package libav

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/govdbot/govd/internal/logger"
)

func TestRunPipedRemux(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	logger.Init()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	out := filepath.Join(dir, "out.mp4")
	gen := exec.Command("ffmpeg", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=64x64:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "mpeg4", "-c:a", "aac", "-shortest", in)
	if b, err := gen.CombinedOutput(); err != nil {
		t.Skipf("cannot generate sample: %v: %s", err, b)
	}

	if err := RemuxFileWithDoublePass(in, out); err != nil {
		t.Fatalf("remux failed: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil || st.Size() == 0 {
		t.Fatalf("output missing: %v", err)
	}
	probe, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", "stream=codec_type", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if got := strings.Fields(string(probe)); len(got) != 2 {
		t.Fatalf("expected 2 streams, got %v", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("unexpected leftover files: %v", entries)
	}
}

func TestRunPipedRemuxInvalidInput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	logger.Init()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.mp4")
	out := filepath.Join(dir, "out.mp4")
	os.WriteFile(in, []byte("garbage"), 0o644)
	err := RemuxFileWithDoublePass(in, out)
	if err == nil {
		t.Fatal("expected error")
	}
	if _, statErr := os.Stat(out); statErr == nil {
		t.Fatal("output should have been removed")
	}
	t.Logf("error: %v", err)
}
