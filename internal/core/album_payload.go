package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"

	"github.com/govdbot/govd/internal/database"
)

const (
	telegramStartPayloadMax   = 64
	telegramCallbackDataMax   = 64
	albumResendCallbackPrefix = "album:rs:"
)

// BuildAlbumStartPayload returns a Telegram /start payload for an album deep link.
// Format: a_<extractorID>_<contentID>, or a_h_<hash> when too long / invalid chars.
func BuildAlbumStartPayload(ctx context.Context, extractorID, contentID string) (string, error) {
	direct := fmt.Sprintf("a_%s_%s", extractorID, contentID)
	if len(direct) <= telegramStartPayloadMax && isValidStartPayload(direct) {
		return direct, nil
	}
	return storeAlbumPayloadHash(ctx, extractorID, contentID)
}

// BuildAlbumResendCallbackData builds callback_data for the resend button.
// Format: album:rs:<startPayload>. Falls back to hashed start payload when needed
// so the result fits Telegram's 64-byte callback_data limit.
func BuildAlbumResendCallbackData(ctx context.Context, extractorID, contentID string) (string, error) {
	payload, err := BuildAlbumStartPayload(ctx, extractorID, contentID)
	if err != nil {
		return "", err
	}
	data := albumResendCallbackPrefix + payload
	if len(data) <= telegramCallbackDataMax {
		return data, nil
	}
	hashed, err := storeAlbumPayloadHash(ctx, extractorID, contentID)
	if err != nil {
		return "", err
	}
	data = albumResendCallbackPrefix + hashed
	if len(data) > telegramCallbackDataMax {
		return "", fmt.Errorf("album resend callback data too long: %d", len(data))
	}
	return data, nil
}

// ParseAlbumResendCallbackData resolves album:rs:… callback data into IDs.
func ParseAlbumResendCallbackData(ctx context.Context, data string) (extractorID, contentID string, ok bool) {
	data = strings.TrimSpace(data)
	if !strings.HasPrefix(data, albumResendCallbackPrefix) {
		return "", "", false
	}
	return ParseAlbumStartPayload(ctx, strings.TrimPrefix(data, albumResendCallbackPrefix))
}

// ParseAlbumStartPayload resolves a start payload into extractorID + contentID.
func ParseAlbumStartPayload(ctx context.Context, payload string) (extractorID, contentID string, ok bool) {
	payload = strings.TrimSpace(payload)
	if !strings.HasPrefix(payload, "a_") {
		return "", "", false
	}
	rest := strings.TrimPrefix(payload, "a_")
	if strings.HasPrefix(rest, "h_") {
		hash := strings.TrimPrefix(rest, "h_")
		if hash == "" {
			return "", "", false
		}
		row, err := database.Q().GetInlineAlbumPayload(ctx, hash)
		if err != nil {
			return "", "", false
		}
		return row.ExtractorID, row.ContentID, true
	}
	idx := strings.IndexByte(rest, '_')
	if idx <= 0 || idx >= len(rest)-1 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}

func storeAlbumPayloadHash(ctx context.Context, extractorID, contentID string) (string, error) {
	sum := sha256.Sum256([]byte(extractorID + "\x00" + contentID))
	hash := hex.EncodeToString(sum[:8]) // 16 hex chars
	payload := "a_h_" + hash
	err := database.Q().UpsertInlineAlbumPayload(ctx, database.UpsertInlineAlbumPayloadParams{
		Hash:        hash,
		ExtractorID: extractorID,
		ContentID:   contentID,
	})
	if err != nil {
		return "", fmt.Errorf("store album payload hash: %w", err)
	}
	return payload, nil
}

func isValidStartPayload(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
