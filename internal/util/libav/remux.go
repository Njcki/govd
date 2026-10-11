package libav

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/govdbot/govd/internal/extractors/twitter"
	"github.com/govdbot/govd/internal/logger"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

func RemuxFile(inputPath string, outputPath string) error {
	isVork := twitter.IsVorkMuxer(inputPath)
	if isVork {
		return RemuxFileWithDoublePass(inputPath, outputPath)
	}

	logger.L.Debugf("remuxing file: %s", inputPath)

	err := ffmpeg.Input(inputPath).
		Output(outputPath, ffmpeg.KwArgs{
			"map":      "0",
			"c":        "copy",
			"movflags": "+faststart",
		}).
		Silent(true).
		OverWriteOutput().
		Run()

	if err != nil {
		os.Remove(outputPath)
		return err
	}
	return nil
}

// RemuxFileWithDoublePass fixes files produced by the Twitter "vork"
// muxer (see govdbot/govd#29) by remuxing mp4 -> matroska -> mp4.
// the intermediate matroska stream is piped between two ffmpeg
// processes instead of being written to a temporary file, which
// produces identical output while saving a full disk write+read.
func RemuxFileWithDoublePass(inputPath string, outputPath string) error {
	logger.L.Debugf("remuxing file with double pass (piped): %s", inputPath)

	err := runPipedRemux(inputPath, outputPath)
	if err != nil {
		os.Remove(outputPath)
		return err
	}
	return nil
}

const maxStderrBytes = 4096

// boundedBuffer keeps at most maxStderrBytes of ffmpeg stderr.
type boundedBuffer struct {
	buf bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := maxStderrBytes - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return strings.TrimSpace(b.buf.String())
}

func runPipedRemux(inputPath string, outputPath string) error {
	pr, pw, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}

	var stderr1, stderr2 boundedBuffer

	first := exec.Command(
		"ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", inputPath,
		"-map", "0", "-c", "copy",
		"-f", "matroska", "-",
	)
	first.Stdout = pw
	first.Stderr = &stderr1

	second := exec.Command(
		"ffmpeg", "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", "-",
		"-map", "0", "-c", "copy",
		"-movflags", "+faststart",
		"-y", outputPath,
	)
	second.Stdin = pr
	second.Stderr = &stderr2

	if err := second.Start(); err != nil {
		pr.Close()
		pw.Close()
		return fmt.Errorf("failed to start ffmpeg (mux): %w", err)
	}
	if err := first.Start(); err != nil {
		pr.Close()
		pw.Close()
		second.Process.Kill()
		second.Wait()
		return fmt.Errorf("failed to start ffmpeg (demux): %w", err)
	}
	// the child processes own their copies of the pipe ends now
	pr.Close()
	pw.Close()

	errFirst := make(chan error, 1)
	errSecond := make(chan error, 1)
	go func() { errFirst <- first.Wait() }()
	go func() { errSecond <- second.Wait() }()

	var e1, e2 error
	gotFirst, gotSecond := false, false
	for !gotFirst || !gotSecond {
		select {
		case e1 = <-errFirst:
			gotFirst = true
			if e1 != nil && !gotSecond {
				second.Process.Kill()
			}
		case e2 = <-errSecond:
			gotSecond = true
			if e2 != nil && !gotFirst {
				first.Process.Kill()
			}
		}
	}

	switch {
	case e1 != nil && stderr1.String() != "":
		return fmt.Errorf("ffmpeg (demux) failed: %w: %s", e1, stderr1.String())
	case e2 != nil && stderr2.String() != "":
		return fmt.Errorf("ffmpeg (mux) failed: %w: %s", e2, stderr2.String())
	case e1 != nil:
		return fmt.Errorf("ffmpeg (demux) failed: %w", e1)
	case e2 != nil:
		return fmt.Errorf("ffmpeg (mux) failed: %w", e2)
	}
	return nil
}
