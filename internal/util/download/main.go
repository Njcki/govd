package download

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/networking"
	"github.com/govdbot/govd/internal/util/download/chunked"
	"github.com/govdbot/govd/internal/util/download/segmented"
	"github.com/govdbot/govd/internal/util/libav"
)

func DownloadFile(
	ctx *models.ExtractorContext,
	urlList []string,
	fileName string,
	settings *models.DownloadSettings,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("nil extractor context")
	}
	settings = ensureDownloadSettings(settings)
	ensureDownloadDir()

	client := ctx.HTTPClient.AsDownloadClient()

	filePath := ToPath(fileName)
	ctx.FilesTracker.Add(filePath)

	file, err := os.Create(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var lastErr error
	maxAttempts := max(settings.Retries, 1)
	for _, url := range urlList {
		for attempt := range maxAttempts {
			if attempt > 0 {
				backoff := time.Duration(attempt) * time.Second
				ctx.Warnf("retrying download from %s (attempt %d/%d) after %s: %v",
					url, attempt+1, maxAttempts, backoff, lastErr)
				select {
				case <-ctx.Context.Done():
					return "", ctx.Context.Err()
				case <-time.After(backoff):
				}
			} else {
				ctx.Debugf("attempting download from: %s", url)
			}

			if err := file.Truncate(0); err != nil {
				return "", err
			}
			if _, err := file.Seek(0, 0); err != nil {
				return "", err
			}

			cd, err := chunked.New(ctx.Context, client, url, settings)
			if err != nil {
				// ranged requests not supported, fallback to sequential download
				err = downloadSequential(ctx, client, url, file, settings)
			} else {
				err = cd.Download(ctx, file, settings.NumConnections)
			}
			if err != nil {
				lastErr = err
				if isTransientDownloadError(err) && attempt+1 < maxAttempts {
					continue
				}
				break // try next URL
			}

			outputPath := strings.TrimSuffix(
				filePath,
				filepath.Ext(filePath),
			) + "_remuxed" + filepath.Ext(filePath)
			ctx.FilesTracker.Add(outputPath)

			err = libav.RemuxFile(filePath, outputPath)
			if err != nil {
				ctx.Warnf("remuxing failed, using original file: %v", err)
				return filePath, nil
			}

			// replace original file with remuxed file
			os.Rename(outputPath, filePath)

			return filePath, nil
		}
	}

	return "", lastErr
}

// isTransientDownloadError reports whether err is likely caused by a
// mid-transfer CDN/stream reset (HTTP/2 PROTOCOL_ERROR, connection reset)
// and worth retrying the same URL.
func isTransientDownloadError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"protocol_error",
		"stream error",
		"http2:",
		"connection reset",
		"broken pipe",
		"unexpected eof",
		"server closed idle connection",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func DownloadFileWithSegments(
	ctx *models.ExtractorContext,
	initSegmentURL string,
	segmentURLs []string,
	fileName string,
	settings *models.DownloadSettings,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("nil extractor context")
	}
	settings = ensureDownloadSettings(settings)
	ensureDownloadDir()

	client := ctx.HTTPClient.AsDownloadClient()

	filePath := ToPath(fileName)
	ctx.FilesTracker.Add(filePath)

	tempDir := ToPath("segments" + uuid.NewString()[:8])
	ctx.FilesTracker.Add(tempDir)

	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}

	ctx.Debugf("attempting download from: %s", segmentURLs[0])

	sd := segmented.New(
		ctx.Context, client,
		tempDir, segmentURLs,
		&segmented.SegmentedDownloaderOptions{
			InitSegment:      initSegmentURL,
			DownloadSettings: settings,
		},
	)

	file, err := os.Create(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	err = sd.Download(ctx.Context, file, settings.NumConnections)
	if err != nil {
		return "", err
	}

	outputPath := strings.TrimSuffix(
		filePath,
		filepath.Ext(filePath),
	) + "_remuxed" + filepath.Ext(filePath)
	ctx.FilesTracker.Add(outputPath)

	err = libav.RemuxFile(filePath, outputPath)
	if err != nil {
		ctx.Warnf("remuxing failed, using original file: %v", err)
		return filePath, nil
	}

	// replace original file with remuxed file
	os.Rename(outputPath, filePath)

	return filePath, nil
}

func DownloadFileInMemory(
	ctx *models.ExtractorContext,
	urlList []string,
	settings *models.DownloadSettings,
) (*bytes.Reader, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil extractor context")
	}
	settings = ensureDownloadSettings(settings)

	client := ctx.HTTPClient.AsDownloadClient()
	maxRetries := max(settings.Retries, 1)

	var lastErr error
	for _, url := range urlList {
		for attempt := range maxRetries {
			ctx.Debugf("attempting download from: %s (attempt %d/%d)", url, attempt+1, maxRetries)
			resp, err := client.FetchWithContext(
				ctx.Context,
				http.MethodGet,
				url, &networking.RequestParams{
					Headers: settings.Headers,
					Cookies: settings.Cookies,
				},
			)
			if err != nil {
				lastErr = err
				if ctx.Context.Err() != nil {
					// context expired or cancelled: retrying is pointless
					return nil, fmt.Errorf("all download attempts failed: %w", err)
				}
				continue
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
				continue
			}

			data, err := io.ReadAll(resp.Body)
			if err != nil {
				resp.Body.Close()
				lastErr = err
				continue
			}

			resp.Body.Close()
			return bytes.NewReader(data), nil
		}
	}

	if lastErr != nil {
		return nil, fmt.Errorf("all download attempts failed: %w", lastErr)
	}
	return nil, fmt.Errorf("all download attempts failed")
}

func downloadSequential(
	ctx *models.ExtractorContext,
	client *networking.HTTPClient,
	url string,
	writer io.Writer,
	settings *models.DownloadSettings,
) error {
	settings = ensureDownloadSettings(settings)

	resp, err := client.FetchWithContext(
		ctx.Context,
		http.MethodGet, url,
		&networking.RequestParams{
			Headers: settings.Headers,
			Cookies: settings.Cookies,
		},
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	_, err = io.Copy(writer, resp.Body)
	if err != nil {
		return err
	}

	return nil
}
