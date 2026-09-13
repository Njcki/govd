package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/extractors"
	"github.com/govdbot/govd/internal/localization"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/util"
	"github.com/jackc/pgx/v5"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

// HandleInlineAlbumOpen sends the full album on first open, or replies to the
// previously saved album message on later opens.
func HandleInlineAlbumOpen(
	bot *gotgbot.Bot,
	ctx *ext.Context,
	extractorID, contentID string,
) error {
	chat, err := util.ChatFromContext(ctx)
	if err != nil {
		return err
	}
	localizer := localization.New(chat.Language)
	userID := ctx.EffectiveUser.Id

	ref, err := database.Q().GetInlineAlbumRef(context.Background(), database.GetInlineAlbumRefParams{
		UserID:      userID,
		ExtractorID: extractorID,
		ContentID:   contentID,
	})
	if err == nil {
		already := localizer.T(&i18n.LocalizeConfig{
			MessageID: localization.AlbumAlreadySentMessage.ID,
		})
		_, sendErr := bot.SendMessage(ctx.EffectiveChat.Id, already, &gotgbot.SendMessageOpts{
			ReplyParameters: &gotgbot.ReplyParameters{
				MessageId: ref.MessageID,
			},
		})
		if sendErr == nil {
			return nil
		}
		if !isReplyTargetMissing(sendErr) {
			return sendErr
		}
		logger.L.Warnf("album ref message missing for user=%d %s/%s, re-sending", userID, extractorID, contentID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	extractor := extractors.ByID(extractorID)
	if extractor == nil {
		return fmt.Errorf("unknown extractor: %s", extractorID)
	}

	mediaRow, err := database.Q().GetMediaByContentID(context.Background(), database.GetMediaByContentIDParams{
		ExtractorID: extractorID,
		ContentID:   contentID,
	})
	if err != nil {
		return fmt.Errorf("album media not cached: %w", err)
	}

	media, err := ParseStoredMedia(context.Background(), extractor, &mediaRow)
	if err != nil {
		return err
	}
	if len(media.Items) == 0 {
		return ErrNoMedia
	}

	formats := make([]*models.DownloadedFormat, 0, len(media.Items))
	for i, item := range media.Items {
		formats = append(formats, &models.DownloadedFormat{
			Format: item.Formats[0],
			Index:  i,
		})
	}

	caption := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.AlbumCompleteMessage.ID,
	})

	extractorCtx := &models.ExtractorContext{
		ContentID:    contentID,
		ContentURL:   media.ContentURL,
		Extractor:    extractor,
		Chat:         chat,
		Context:      context.Background(),
		CancelFunc:   func() {},
		FilesTracker: models.NewFilesTracker(),
	}

	messages, err := SendFormats(
		bot, ctx, extractorCtx,
		media, formats,
		&models.SendFormatsOptions{
			Caption:  caption,
			IsStored: true,
		},
	)
	if err != nil {
		return err
	}

	err = database.Q().UpsertInlineAlbumRef(context.Background(), database.UpsertInlineAlbumRefParams{
		UserID:      userID,
		ExtractorID: extractorID,
		ContentID:   contentID,
		MessageID:   messages[0].MessageId,
	})
	if err != nil {
		logger.L.Errorf("failed to save inline album ref: %v", err)
	}
	return nil
}

func isReplyTargetMissing(err error) bool {
	var tgErr *gotgbot.TelegramError
	if !errors.As(err, &tgErr) {
		return false
	}
	desc := strings.ToLower(tgErr.Description)
	return strings.Contains(desc, "message to reply not found") ||
		strings.Contains(desc, "replied message not found") ||
		strings.Contains(desc, "message not found")
}
