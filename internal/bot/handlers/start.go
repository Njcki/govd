package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/govdbot/govd/internal/config"
	"github.com/govdbot/govd/internal/core"
	"github.com/govdbot/govd/internal/localization"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/util"
	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
)

func StartHandler(bot *gotgbot.Bot, ctx *ext.Context) error {
	if ctx.EffectiveChat.Type != gotgbot.ChatTypePrivate {
		return HandleGroupStart(bot, ctx)
	}

	if payload := albumStartPayload(ctx); payload != "" {
		extractorID, contentID, ok := core.ParseAlbumStartPayload(context.Background(), payload)
		if ok {
			err := core.HandleInlineAlbumOpen(bot, ctx, extractorID, contentID)
			if err != nil {
				logger.L.Errorf("inline album open failed: %v", err)
				chat, chatErr := util.ChatFromContext(ctx)
				if chatErr == nil {
					localizer := localization.New(chat.Language)
					ctx.EffectiveMessage.Reply(
						bot,
						localizer.T(&i18n.LocalizeConfig{
							MessageID: localization.ErrorMessage.ID,
						}),
						nil,
					)
				}
			}
			return nil
		}
	}

	user := ctx.EffectiveUser

	chat, err := util.ChatFromContext(ctx)
	if err != nil {
		return err
	}
	localizer := localization.New(chat.Language)

	keyboard := getStartKeyboard(bot, localizer)

	text := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.StartMessage.ID,
		TemplateData: map[string]string{
			"Name": util.MentionUser(user),
		},
	})

	if ctx.Message != nil {
		ctx.EffectiveMessage.Reply(
			bot, text,
			&gotgbot.SendMessageOpts{
				ReplyMarkup: keyboard,
			},
		)
	} else if ctx.CallbackQuery != nil {
		ctx.CallbackQuery.Answer(bot, nil)
		ctx.EffectiveMessage.EditText(
			bot, text,
			&gotgbot.EditMessageTextOpts{
				ReplyMarkup: keyboard,
			},
		)
	}
	return nil
}

func albumStartPayload(ctx *ext.Context) string {
	if ctx.Message == nil {
		return ""
	}
	args := ctx.Args()
	if len(args) < 2 {
		return ""
	}
	payload := strings.TrimSpace(args[1])
	if strings.HasPrefix(payload, "a_") {
		return payload
	}
	return ""
}

func getStartKeyboard(
	bot *gotgbot.Bot,
	localizer *localization.Localizer,
) gotgbot.InlineKeyboardMarkup {
	addButton := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.AddButton.ID,
	})
	settingsButton := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.SettingsButton.ID,
	})
	extractorsButton := localizer.T(&i18n.LocalizeConfig{
		MessageID: localization.ExtractorsButton.ID,
	})
	return gotgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
			{
				{
					Text: addButton,
					Url: fmt.Sprintf(
						"https://t.me/%s?startgroup=true",
						bot.Username,
					),
				},
			},
			{
				{
					Text:         settingsButton,
					CallbackData: "settings",
				},
				{
					Text:         extractorsButton,
					CallbackData: "extractors",
				},
			},
			{
				{
					Text: "github",
					Url:  config.Env.RepoURL,
				},
			},
		},
	}
}

func HandleGroupStart(bot *gotgbot.Bot, ctx *ext.Context) error {
	ctx.EffectiveMessage.Reply(bot, "✅", nil)
	return nil
}
