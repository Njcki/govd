package handlers

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PaulSonOfLars/gotgbot/v2"
	"github.com/PaulSonOfLars/gotgbot/v2/ext"
	"github.com/PaulSonOfLars/gotgbot/v2/ext/handlers/filters/message"
	"github.com/govdbot/govd/internal/logger"
	"github.com/govdbot/govd/internal/util"
)

const (
	igCookieFileName  = "instagram.txt"
	igCredKeyUsername = "IG_USERNAME"
	igCredKeyPassword = "IG_PASSWORD"
	igMaxCookieBytes  = 2 << 20 // 2 MiB
	igPendingTTL      = 10 * time.Minute

	igCBUpload = "igcookies:upload"
	igCBUser   = "igcookies:user"
	igCBPass   = "igcookies:pass"
	igCBClear  = "igcookies:clear"
	igCBStatus = "igcookies:status"
	igCBOpen   = "igcookies:open"
)

type igPendingKind int

const (
	igPendingNone igPendingKind = iota
	igPendingCookie
	igPendingUsername
	igPendingPassword
)

type igPending struct {
	kind      igPendingKind
	expiresAt time.Time
}

var (
	igCookiePath      = "private/cookies/instagram.txt"
	igCredentialsPath = "private/ig_credentials.env"

	igPendingMu sync.Mutex
	igPendingBy = map[int64]igPending{}
)

func setIGPending(userID int64, kind igPendingKind) {
	igPendingMu.Lock()
	defer igPendingMu.Unlock()
	igPendingBy[userID] = igPending{
		kind:      kind,
		expiresAt: time.Now().Add(igPendingTTL),
	}
}

func clearIGPending(userID int64) {
	igPendingMu.Lock()
	defer igPendingMu.Unlock()
	delete(igPendingBy, userID)
}

func getIGPending(userID int64) igPendingKind {
	igPendingMu.Lock()
	defer igPendingMu.Unlock()
	p, ok := igPendingBy[userID]
	if !ok {
		return igPendingNone
	}
	if time.Now().After(p.expiresAt) {
		delete(igPendingBy, userID)
		return igPendingNone
	}
	return p.kind
}

func IGCookiesPendingFilter(msg *gotgbot.Message) bool {
	if msg == nil || msg.From == nil {
		return false
	}
	if msg.Chat.Type != gotgbot.ChatTypePrivate {
		return false
	}
	kind := getIGPending(msg.From.Id)
	if kind == igPendingNone {
		return false
	}
	if kind == igPendingCookie {
		return msg.Document != nil
	}
	// username / password: plain text, not a command
	return message.Text(msg) && !message.Command(msg)
}

func IGCookiesCommandHandler(bot *gotgbot.Bot, ctx *ext.Context) error {
	if !util.IsBotAdmin(ctx) {
		return ext.EndGroups
	}
	text := buildIGCookiesStatusText()
	opts := &gotgbot.SendMessageOpts{
		ReplyMarkup: igCookiesKeyboard(),
		ParseMode:   gotgbot.ParseModeHTML,
	}
	if ctx.Message != nil {
		ctx.EffectiveMessage.Reply(bot, text, opts)
	}
	return ext.EndGroups
}

func IGCookiesCallbackHandler(bot *gotgbot.Bot, ctx *ext.Context) error {
	if !util.IsBotAdmin(ctx) {
		if ctx.CallbackQuery != nil {
			ctx.CallbackQuery.Answer(bot, &gotgbot.AnswerCallbackQueryOpts{
				Text:      "Non autorizzato.",
				ShowAlert: true,
			})
		}
		return nil
	}

	data := ""
	if ctx.CallbackQuery != nil {
		data = ctx.CallbackQuery.Data
	}
	userID := ctx.EffectiveUser.Id

	switch data {
	case igCBOpen:
		ctx.CallbackQuery.Answer(bot, nil)
		ctx.EffectiveMessage.Reply(bot, buildIGCookiesStatusText(), &gotgbot.SendMessageOpts{
			ReplyMarkup: igCookiesKeyboard(),
			ParseMode:   gotgbot.ParseModeHTML,
		})
	case igCBUpload:
		setIGPending(userID, igPendingCookie)
		ctx.CallbackQuery.Answer(bot, nil)
		ctx.EffectiveMessage.Reply(bot,
			"Invia ora il file cookie Netscape (<code>instagram.txt</code>) come documento.",
			&gotgbot.SendMessageOpts{ParseMode: gotgbot.ParseModeHTML},
		)
	case igCBUser:
		setIGPending(userID, igPendingUsername)
		ctx.CallbackQuery.Answer(bot, nil)
		ctx.EffectiveMessage.Reply(bot, "Invia ora lo username Instagram (testo).", nil)
	case igCBPass:
		setIGPending(userID, igPendingPassword)
		ctx.CallbackQuery.Answer(bot, nil)
		ctx.EffectiveMessage.Reply(bot, "Invia ora la password Instagram (testo). Verrà salvata in locale e non loggata.", nil)
	case igCBClear:
		clearIGPending(userID)
		err := deleteIGCredentials()
		ctx.CallbackQuery.Answer(bot, nil)
		if err != nil {
			ctx.EffectiveMessage.Reply(bot, "Errore cancellazione credenziali.", nil)
			logger.L.Warnf("ig credentials delete failed: %v", err)
		} else {
			ctx.EffectiveMessage.Reply(bot, "Credenziali cancellate.", nil)
		}
		refreshIGCookiesPanel(bot, ctx)
	case igCBStatus:
		ctx.CallbackQuery.Answer(bot, nil)
		refreshIGCookiesPanel(bot, ctx)
	default:
		ctx.CallbackQuery.Answer(bot, nil)
	}
	return nil
}

func IGCookiesPendingHandler(bot *gotgbot.Bot, ctx *ext.Context) error {
	if !util.IsBotAdmin(ctx) {
		return ext.ContinueGroups
	}
	userID := ctx.EffectiveUser.Id
	kind := getIGPending(userID)
	if kind == igPendingNone {
		return ext.ContinueGroups
	}

	msg := ctx.EffectiveMessage
	switch kind {
	case igPendingCookie:
		if msg.Document == nil {
			return ext.ContinueGroups
		}
		err := saveIGCookieDocument(bot, msg.Document)
		clearIGPending(userID)
		tryDeleteSensitiveMessage(bot, msg)
		if err != nil {
			msg.Reply(bot, "Errore cookie: "+util.Unquote(err.Error()), nil)
			logger.L.Warnf("ig cookie upload failed: %v", err)
			return ext.EndGroups
		}
		util.InvalidateCookieCache(igCookieFileName)
		msg.Reply(bot, "Cookie Instagram salvati e cache ricaricata.", nil)
		return ext.EndGroups

	case igPendingUsername:
		username := strings.TrimSpace(msg.Text)
		if username == "" {
			msg.Reply(bot, "Username vuoto. Riprova.", nil)
			return ext.EndGroups
		}
		err := upsertIGCredential(igCredKeyUsername, username)
		clearIGPending(userID)
		tryDeleteSensitiveMessage(bot, msg)
		if err != nil {
			msg.Reply(bot, "Errore salvataggio username.", nil)
			logger.L.Warnf("ig username save failed: %v", err)
			return ext.EndGroups
		}
		msg.Reply(bot, "Username salvato.", nil)
		return ext.EndGroups

	case igPendingPassword:
		password := strings.TrimSpace(msg.Text)
		if password == "" {
			msg.Reply(bot, "Password vuota. Riprova.", nil)
			return ext.EndGroups
		}
		err := upsertIGCredential(igCredKeyPassword, password)
		clearIGPending(userID)
		tryDeleteSensitiveMessage(bot, msg)
		if err != nil {
			msg.Reply(bot, "Errore salvataggio password.", nil)
			logger.L.Warnf("ig password save failed")
			return ext.EndGroups
		}
		msg.Reply(bot, "Password salvata.", nil)
		return ext.EndGroups
	}

	return ext.ContinueGroups
}

func refreshIGCookiesPanel(bot *gotgbot.Bot, ctx *ext.Context) {
	text := buildIGCookiesStatusText()
	_, _, err := ctx.EffectiveMessage.EditText(bot, text, &gotgbot.EditMessageTextOpts{
		ReplyMarkup: igCookiesKeyboard(),
		ParseMode:   gotgbot.ParseModeHTML,
	})
	if err != nil {
		ctx.EffectiveMessage.Reply(bot, text, &gotgbot.SendMessageOpts{
			ReplyMarkup: igCookiesKeyboard(),
			ParseMode:   gotgbot.ParseModeHTML,
		})
	}
}

func buildIGCookiesStatusText() string {
	cookieExists := fileExists(igCookiePath)
	hasSessionID := false
	if cookieExists {
		hasSessionID = cookieFileHasSessionID(igCookiePath)
	}
	credsExist := fileExists(igCredentialsPath)

	yesNo := func(v bool) string {
		if v {
			return "sì"
		}
		return "no"
	}

	return fmt.Sprintf(
		"<b>Cookie / credenziali Instagram</b>\n\n"+
			"File cookie (<code>%s</code>): <b>%s</b>\n"+
			"Contiene <code>sessionid</code>: <b>%s</b>\n"+
			"File credenziali: <b>%s</b>\n\n"+
			"<i>Solo storage locale. Nessun login automatico Instagram.</i>",
		igCookiePath,
		yesNo(cookieExists),
		yesNo(hasSessionID),
		yesNo(credsExist),
	)
}

func igCookiesKeyboard() gotgbot.InlineKeyboardMarkup {
	return gotgbot.InlineKeyboardMarkup{
		InlineKeyboard: [][]gotgbot.InlineKeyboardButton{
			{
				{Text: "Invia file cookie", CallbackData: igCBUpload},
			},
			{
				{Text: "Imposta username", CallbackData: igCBUser},
				{Text: "Imposta password", CallbackData: igCBPass},
			},
			{
				{Text: "Cancella credenziali", CallbackData: igCBClear},
				{Text: "Stato", CallbackData: igCBStatus},
			},
		},
	}
}

func saveIGCookieDocument(bot *gotgbot.Bot, doc *gotgbot.Document) error {
	if doc.FileSize > igMaxCookieBytes {
		return fmt.Errorf("file troppo grande (max 2MB)")
	}

	f, err := bot.GetFile(doc.FileId, nil)
	if err != nil {
		return fmt.Errorf("getFile: %w", err)
	}
	if f.FilePath == "" {
		return fmt.Errorf("file path vuoto")
	}

	url := f.URL(bot, nil)
	resp, err := http.Get(url) //nolint:gosec // Telegram Bot API file URL
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}

	limited := io.LimitReader(resp.Body, igMaxCookieBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	if len(data) > igMaxCookieBytes {
		return fmt.Errorf("file troppo grande (max 2MB)")
	}

	content := string(data)
	if !netscapeHasSessionID(content) {
		return fmt.Errorf("manca sessionid nel file")
	}

	if err := os.MkdirAll(filepath.Dir(igCookiePath), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	tmp := igCookiePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, igCookiePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	_ = os.Chmod(igCookiePath, 0o600)
	return nil
}

func netscapeHasSessionID(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Netscape: domain \t flag \t path \t secure \t expiry \t name \t value
		parts := strings.Split(line, "\t")
		if len(parts) >= 7 {
			if parts[5] == "sessionid" {
				return true
			}
			continue
		}
		// fallback: token presence as whole field
		fields := strings.Fields(line)
		for _, f := range fields {
			if f == "sessionid" {
				return true
			}
		}
	}
	return false
}

func cookieFileHasSessionID(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return netscapeHasSessionID(string(data))
}

func readIGCredentials() (map[string]string, error) {
	out := map[string]string{}
	data, err := os.ReadFile(igCredentialsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(key)] = strings.TrimSpace(val)
	}
	return out, nil
}

func writeIGCredentials(creds map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(igCredentialsPath), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# Instagram credentials — local only, never commit\n")
	if v, ok := creds[igCredKeyUsername]; ok && v != "" {
		b.WriteString(igCredKeyUsername + "=" + v + "\n")
	}
	if v, ok := creds[igCredKeyPassword]; ok && v != "" {
		b.WriteString(igCredKeyPassword + "=" + v + "\n")
	}
	tmp := igCredentialsPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, igCredentialsPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(igCredentialsPath, 0o600)
	return nil
}

func upsertIGCredential(key, value string) error {
	creds, err := readIGCredentials()
	if err != nil {
		return err
	}
	creds[key] = value
	return writeIGCredentials(creds)
}

func deleteIGCredentials() error {
	err := os.Remove(igCredentialsPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func tryDeleteSensitiveMessage(bot *gotgbot.Bot, msg *gotgbot.Message) {
	if msg == nil {
		return
	}
	_, _ = msg.Delete(bot, nil)
}
