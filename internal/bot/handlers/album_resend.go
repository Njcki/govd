package handlers

import (
	"context"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/govdbot/govd/internal/core"
	"github.com/govdbot/govd/internal/localization"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/util"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

func AlbumResendHandler(bot *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.CallbackQuery == nil {
		return nil
	}
	ctx.CallbackQuery.Answer(bot, nil)

	extractorID, contentID, ok := core.ParseAlbumResendCallbackData(
		context.Background(),
		ctx.CallbackQuery.Data,
	)
	if !ok {
		return nil
	}

	err := core.HandleInlineAlbumResend(bot, ctx, extractorID, contentID)
	if err != nil {
		logger.L.Errorf("inline album resend failed: %v", err)
		chat, chatErr := util.ChatFromContext(ctx)
		if chatErr == nil {
			localizer := localization.New(chat.Language)
			bot.SendMessage(
				ctx.EffectiveChat.Id,
				localizer.T(&i18n.LocalizeConfig{
					MessageID: localization.ErrorMessage.ID,
				}),
				nil,
			)
		}
	}
	return nil
}
