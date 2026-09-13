package core

import (
	"context"
	"errors"
	"fmt"

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

// HandleInlineAlbumOpen sends the full album on first open, or a short notice
// with a resend button when a ref already exists.
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
	chatID := ctx.EffectiveChat.Id

	_, err = database.Q().GetInlineAlbumRef(context.Background(), database.GetInlineAlbumRefParams{
		UserID:      userID,
		ExtractorID: extractorID,
		ContentID:   contentID,
	})
	if err == nil {
		return sendAlreadySentNotice(bot, chatID, localizer, extractorID, contentID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	return sendAndStoreInlineAlbum(bot, ctx, chat, localizer, userID, extractorID, contentID)
}

// HandleInlineAlbumResend always re-sends the full album and updates the ref.
func HandleInlineAlbumResend(
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
	return sendAndStoreInlineAlbum(bot, ctx, chat, localizer, userID, extractorID, contentID)
}

func sendAlreadySentNotice(
	bot *gotgbot.Bot,
	chatID int64,
	localizer *localization.Localizer,
	extractorID, contentID string,
) error {
	text := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.AlbumAlreadySentMessage.ID,
	})
	buttonText := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.ResendAlbumButton.ID,
	})
	callbackData, err := BuildAlbumResendCallbackData(context.Background(), extractorID, contentID)
	if err != nil {
		return err
	}
	_, err = bot.SendMessage(chatID, text, &gotgbot.SendMessageOpts{
		ReplyMarkup: gotgbot.InlineKeyboardMarkup{
			InlineKeyboard: [][]gotgbot.InlineKeyboardButton{{
				{
					Text:         buttonText,
					CallbackData: callbackData,
				},
			}},
		},
	})
	return err
}

func sendAndStoreInlineAlbum(
	bot *gotgbot.Bot,
	ctx *ext.Context,
	chat *database.GetOrCreateChatRow,
	localizer *localization.Localizer,
	userID int64,
	extractorID, contentID string,
) error {
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
		if len(item.Formats) == 0 {
			continue
		}
		formats = append(formats, &models.DownloadedFormat{
			Format: item.Formats[0],
			Index:  i,
		})
	}
	if len(formats) == 0 {
		return ErrNoMedia
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

	// Send without replying to /start so the saved message_id is a clean album root.
	messages, err := sendAlbumToChat(bot, ctx.EffectiveChat.Id, media, formats, caption)
	if err != nil {
		// Fallback to the shared sender if direct send fails.
		messages, err = SendFormats(
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

func sendAlbumToChat(
	bot *gotgbot.Bot,
	chatID int64,
	media *models.Media,
	formats []*models.DownloadedFormat,
	caption string,
) ([]gotgbot.Message, error) {
	var sent []gotgbot.Message
	chunks := chunkFormats(formats, 10)
	for _, chunk := range chunks {
		var input []gotgbot.InputMedia
		for i, f := range chunk {
			cap := ""
			if i == 0 {
				cap = caption
			}
			im, err := f.Format.GetInputMedia(f.FilePath, f.ThumbnailFilePath, cap, false)
			if err != nil {
				return nil, err
			}
			input = append(input, im)
		}
		util.SendMediaAction(bot, chatID, chunk[0].Format.Type)
		msgs, err := bot.SendMediaGroup(chatID, input, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to send media group: %w", err)
		}
		sent = append(sent, msgs...)
	}
	return sent, nil
}

func chunkFormats(formats []*models.DownloadedFormat, size int) [][]*models.DownloadedFormat {
	if size <= 0 {
		size = 10
	}
	var out [][]*models.DownloadedFormat
	for i := 0; i < len(formats); i += size {
		j := i + size
		if j > len(formats) {
			j = len(formats)
		}
		out = append(out, formats[i:j])
	}
	return out
}
