package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/govdbot/govd/internal/config"
	"github.com/govdbot/govd/internal/database"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/localization"
	"github.com/govdbot/govd/internal/models"
	"github.com/govdbot/govd/internal/util"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

func HandleError(
	b *gotgbot.Bot,
	ctx *ext.Context,
	extractorCtx *models.ExtractorContext,
	err error,
) {
	chat := extractorCtx.Chat
	localizer := localization.New(chat.Language)

	botError := asBotError(err)
	if botError != nil {
		sendErrorMessage(
			b, ctx, "",
			localizer.T(&i18n.LocalizeConfig{
				MessageID: botError.ID,
			}),
		)
		if shouldNotifyAdmins(botError.ID) {
			notifyAdmins(b, formatAdminErrorAlert(ctx, extractorCtx, botError.ID, err, ""))
		}
		return
	}

	if errors.Is(err, ErrNoMedia) {
		return
	}
	if isChatWriteForbidden(err) {
		return
	}
	if isPermissionDenied(err) {
		sendErrorMessage(
			b, ctx, "",
			localizer.T(&i18n.LocalizeConfig{
				MessageID: localization.ErrorPermissionDenied.ID,
			}),
		)
		return
	}

	errorID := util.HashedError(err)

	extractorCtx.Errorf("unexpected error: [%s] %v", errorID, err)

	sendErrorMessage(
		b, ctx, errorID,
		localizer.T(&i18n.LocalizeConfig{
			MessageID: localization.ErrorMessage.ID,
		}),
	)

	database.Q().LogError(
		extractorCtx.Context,
		database.LogErrorParams{
			ID:      errorID,
			Message: err.Error(),
		},
	)
	notifyAdmins(b, formatAdminErrorAlert(ctx, extractorCtx, localization.ErrorMessage.ID, err, errorID))
}

func shouldNotifyAdmins(messageID string) bool {
	switch messageID {
	case localization.ErrorInstagramCookies.ID,
		localization.ErrorAuthenticationNeeded.ID:
		return true
	default:
		return false
	}
}

func formatAdminErrorAlert(
	ctx *ext.Context,
	extractorCtx *models.ExtractorContext,
	kind string,
	err error,
	errorID string,
) string {
	var b strings.Builder
	b.WriteString("🛠 <b>govd error report</b>\n")
	b.WriteString("<b>kind:</b> <code>")
	b.WriteString(kind)
	b.WriteString("</code>\n")
	if errorID != "" {
		b.WriteString("<b>id:</b> <code>")
		b.WriteString(errorID)
		b.WriteString("</code>\n")
	}
	if extractorCtx != nil && extractorCtx.Extractor != nil {
		b.WriteString("<b>extractor:</b> <code>")
		b.WriteString(extractorCtx.Extractor.ID)
		b.WriteString("</code>\n")
	}
	if extractorCtx != nil && extractorCtx.ContentURL != "" {
		b.WriteString("<b>url:</b> ")
		b.WriteString(extractorCtx.ContentURL)
		b.WriteString("\n")
	}
	if extractorCtx != nil && extractorCtx.Chat != nil {
		b.WriteString("<b>chat_id:</b> <code>")
		b.WriteString(fmt.Sprintf("%d", extractorCtx.Chat.ChatID))
		b.WriteString("</code>\n")
	}
	if ctx != nil && ctx.EffectiveUser != nil {
		b.WriteString("<b>user_id:</b> <code>")
		b.WriteString(fmt.Sprintf("%d", ctx.EffectiveUser.Id))
		b.WriteString("</code>\n")
	}
	b.WriteString("<b>detail:</b>\n<pre>")
	detail := err.Error()
	if len(detail) > 1500 {
		detail = detail[:1500] + "…"
	}
	b.WriteString(htmlEscape(detail))
	b.WriteString("</pre>")
	return b.String()
}

func htmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
	)
	return replacer.Replace(s)
}

func notifyAdmins(b *gotgbot.Bot, text string) {
	if b == nil || text == "" || len(config.Env.Admins) == 0 {
		return
	}
	for _, adminID := range config.Env.Admins {
		_, err := b.SendMessage(adminID, text, &gotgbot.SendMessageOpts{
			ParseMode: gotgbot.ParseModeHTML,,
			LinkPreviewOptions: &gotgbot.LinkPreviewOptions{
				IsDisabled: true,
			},
		})
		if err != nil {
			logger.L.Warnf("failed to notify admin %d: %v", adminID, err)
		}
	}
}

func isChatWriteForbidden(err error) bool {
	return strings.Contains(err.Error(), "CHAT_WRITE_FORBIDDEN")
}

func isPermissionDenied(err error) bool {
	return strings.Contains(err.Error(), "not enough rights")
}

func asBotError(err error) *util.Error {
	currentErr := err
	for currentErr != nil {
		var botError *util.Error
		if errors.As(currentErr, &botError) {
			return botError
		}
		currentErr = errors.Unwrap(currentErr)
	}
	return nil
}

func formatErrorMessage(ctx *ext.Context, message string, errorID string) string {
	var suffix string
	if errorID != "" {
		if ctx.CallbackQuery != nil || ctx.InlineQuery != nil {
			suffix = " [" + errorID + "]"
		} else {
			suffix = " [<code>" + errorID + "</code>]"
		}
	}
	return "⚠️ " + message + suffix
}

func sendErrorMessage(
	b *gotgbot.Bot,
	ctx *ext.Context,
	errroID string,
	message string,
) {
	message = formatErrorMessage(ctx, message, errroID)

	switch {
	case ctx.Message != nil:
		ctx.EffectiveMessage.Reply(b, message, nil)
	case ctx.CallbackQuery != nil:
		ctx.CallbackQuery.Answer(b, &gotgbot.AnswerCallbackQueryOpts{
			Text:      message,
			ShowAlert: true,
		})
	case ctx.InlineQuery != nil:
		ctx.InlineQuery.Answer(b, nil,
			&gotgbot.AnswerInlineQueryOpts{
				CacheTime: util.Ptr(int64(0)),
				Button: &gotgbot.InlineQueryResultsButton{
					Text:           message,
					StartParameter: "start",
				},
			},
		)
	case ctx.ChosenInlineResult != nil:
		b.EditMessageText(
			message,
			&gotgbot.EditMessageTextOpts{
				InlineMessageId: ctx.ChosenInlineResult.InlineMessageId,
				LinkPreviewOptions: &gotgbot.LinkPreviewOptions{
					IsDisabled: true,
				},
			},
		)
	}
}
