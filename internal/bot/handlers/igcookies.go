package handlers

import (
	"errors"
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
	"github.com/govdbot/govd/internal/networking"
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

	igProbeMu       sync.Mutex
	igProbeCache    igSessionProbe
	igProbeCacheAt  time.Time
	igProbeCacheTTL = 30 * time.Minute
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
	if kind == igPendingCookie {
		if msg.Document != nil {
			return true
		}
		return message.Text(msg) && !message.Command(msg) && looksLikeNetscapeCookieText(msg.Text)
	}
	if kind == igPendingUsername || kind == igPendingPassword {
		return message.Text(msg) && !message.Command(msg)
	}
	// No pending step: accept named cookie documents or pasted Netscape text.
	if msg.Document != nil && looksLikeIGCookieDocument(msg.Document) {
		return true
	}
	return message.Text(msg) && !message.Command(msg) && looksLikeNetscapeCookieText(msg.Text)
}

func looksLikeIGCookieDocument(doc *gotgbot.Document) bool {
	if doc == nil {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(doc.FileName))
	if name == "" {
		return false
	}
	base := filepath.Base(name)
	if base == "instagram.txt" {
		return true
	}
	return strings.Contains(base, "instagram") && strings.HasSuffix(base, ".txt")
}

// looksLikeNetscapeCookieText detects a pasted Instagram cookie jar (portable; no file download).
func looksLikeNetscapeCookieText(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > igMaxCookieBytes {
		return false
	}
	if !strings.Contains(text, "sessionid") {
		return false
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "# netscape http cookie file") {
		return true
	}
	return strings.Contains(lower, ".instagram.com") && strings.Contains(text, "	")
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
			"Invia i cookie Netscape: <b>incolla il testo</b> del file oppure allega <code>instagram.txt</code>.",
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
	msg := ctx.EffectiveMessage

	// Direct DM: pasted Netscape text or named cookie document — no /igcookies step required.
	if kind == igPendingNone {
		err := ingestIGCookieMessage(bot, msg)
		if err != nil {
			if errors.Is(err, errNotIGCookieMessage) {
				return ext.ContinueGroups
			}
			bot.SendMessage(msg.Chat.Id, "❌ Errore cookie: "+util.Unquote(err.Error()), nil)
			logger.L.Warnf("ig cookie auto-upload failed: %v", err)
			tryDeleteSensitiveMessage(bot, msg)
			return ext.EndGroups
		}
		util.InvalidateCookieCache(igCookieFileName)
		confirmIGCookiesInstalled(bot, msg.Chat.Id)
		tryDeleteSensitiveMessage(bot, msg)
		return ext.EndGroups
	}

	switch kind {
	case igPendingCookie:
		err := ingestIGCookieMessage(bot, msg)
		clearIGPending(userID)
		if err != nil {
			bot.SendMessage(msg.Chat.Id, "❌ Errore cookie: "+util.Unquote(err.Error()), nil)
			logger.L.Warnf("ig cookie upload failed: %v", err)
			tryDeleteSensitiveMessage(bot, msg)
			return ext.EndGroups
		}
		util.InvalidateCookieCache(igCookieFileName)
		confirmIGCookiesInstalled(bot, msg.Chat.Id)
		tryDeleteSensitiveMessage(bot, msg)
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

type igSessionProbe struct {
	FilePresent  bool
	HasSessionID bool
	Alive        bool
	Detail       string
	ProbeError   string
}

// probeIGSession returns a cached liveness check (30m) to avoid hammering Instagram.
func probeIGSession() igSessionProbe {
	return probeIGSessionCached(false)
}

func probeIGSessionCached(force bool) igSessionProbe {
	igProbeMu.Lock()
	defer igProbeMu.Unlock()
	if !force && !igProbeCacheAt.IsZero() && time.Since(igProbeCacheAt) < igProbeCacheTTL {
		return igProbeCache
	}
	st := probeIGSessionFresh()
	igProbeCache = st
	igProbeCacheAt = time.Now()
	return st
}

func invalidateIGSessionProbeCache() {
	igProbeMu.Lock()
	defer igProbeMu.Unlock()
	igProbeCacheAt = time.Time{}
}

// probeIGSessionFresh checks whether Instagram still accepts the stored session
// (not just whether the sessionid cookie name is present).
func probeIGSessionFresh() igSessionProbe {
	st := igSessionProbe{FilePresent: fileExists(igCookiePath)}
	if !st.FilePresent {
		st.Detail = "file assente"
		return st
	}
	st.HasSessionID = cookieFileHasSessionID(igCookiePath)
	if !st.HasSessionID {
		st.Detail = "manca sessionid"
		return st
	}

	util.InvalidateCookieCache(igCookieFileName)
	cookies := util.ParseCookieFile(igCookieFileName)
	if len(cookies) == 0 {
		st.Detail = "file non leggibile"
		return st
	}

	// Any media id works: dead sessions redirect to login; live ones return JSON.
	const probeURL = "https://www.instagram.com/api/v1/media/1/info/"
	req, err := http.NewRequest(http.MethodGet, probeURL, nil)
	if err != nil {
		st.ProbeError = "richiesta"
		return st
	}
	req.Header.Set("User-Agent", networking.DefaultUserAgent)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("x-ig-app-id", "936619743392459")
	req.Header.Set("x-asbd-id", "129477")
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	req.Header.Set("Referer", "https://www.instagram.com/")
	for _, c := range cookies {
		if c == nil || c.Name == "" {
			continue
		}
		req.AddCookie(c)
		if c.Name == "csrftoken" && c.Value != "" {
			req.Header.Set("X-CSRFToken", c.Value)
		}
	}

	client := &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		st.ProbeError = "rete"
		logger.L.Warnf("ig session probe failed: %v", err)
		return st
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	loc := resp.Header.Get("Location")
	lowerBody := strings.ToLower(string(body))

	isJSON := strings.Contains(ct, "json") || strings.HasPrefix(strings.TrimSpace(string(body)), "{")
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400 && strings.Contains(strings.ToLower(loc), "login"):
		st.Detail = "redirect a login"
	case strings.Contains(lowerBody, "accounts/login") && strings.Contains(ct, "text/html"):
		st.Detail = "risposta login/html"
	case strings.Contains(lowerBody, "login_required"):
		st.Detail = "login_required"
	case isJSON:
		// Authenticated API responses are JSON even for "media not found" / status=fail.
		// Only treat login_required above as dead.
		st.Alive = true
		st.Detail = "ok"
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		st.Detail = "HTTP " + resp.Status
	case strings.Contains(ct, "text/html"):
		st.Detail = "risposta html"
	default:
		st.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return st
}

func buildIGCookiesStatusText() string {
	credsExist := fileExists(igCredentialsPath)
	st := probeIGSession()

	yesNo := func(v bool) string {
		if v {
			return "sì"
		}
		return "no"
	}

	sessionLine := "Sessione Instagram: <b>no</b>"
	switch {
	case !st.FilePresent:
		sessionLine = "Sessione Instagram: <b>no</b> (file assente)"
	case !st.HasSessionID:
		sessionLine = "Sessione Instagram: <b>no</b> (manca sessionid)"
	case st.Alive:
		sessionLine = "Sessione Instagram: <b>sì</b> (accettata da Instagram)"
	case st.ProbeError != "":
		sessionLine = "Sessione Instagram: <b>non verificabile</b> (" + htmlEscape(st.ProbeError) + ")"
	default:
		sessionLine = "Sessione Instagram: <b>no</b> (" + htmlEscape(st.Detail) + ")"
	}

	return fmt.Sprintf(
		"<b>Cookie / credenziali Instagram</b>\n\n"+
			"File cookie (<code>%s</code>): <b>%s</b>\n"+
			"%s\n"+
			"sessionid nel file: <b>%s</b>\n"+
			"File credenziali: <b>%s</b>\n\n"+
			"Puoi inviarmi i cookie come <b>testo incollato</b> (consigliato) o come documento <code>instagram.txt</code>.\n\n"+
			"<i>Solo storage locale. Nessun login automatico Instagram.</i>",
		igCookiePath,
		yesNo(st.FilePresent),
		sessionLine,
		yesNo(st.HasSessionID),
		yesNo(credsExist),
	)
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
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

var errNotIGCookieMessage = errors.New("not an Instagram cookie message")

func ingestIGCookieMessage(bot *gotgbot.Bot, msg *gotgbot.Message) error {
	if msg == nil {
		return errNotIGCookieMessage
	}
	if msg.Document != nil && (looksLikeIGCookieDocument(msg.Document) || getIGPending(msg.From.Id) == igPendingCookie) {
		data, err := downloadTelegramDocument(bot, msg.Document.FileId)
		if err != nil {
			return fmt.Errorf("%w — se usi Bot API locale, incolla il contenuto come testo", err)
		}
		return installIGCookieContent(string(data))
	}
	if message.Text(msg) && looksLikeNetscapeCookieText(msg.Text) {
		return installIGCookieContent(msg.Text)
	}
	return errNotIGCookieMessage
}

// normalizeNetscapeExpiry rewrites expiration 0 (session) to +1y.
// Android WebView exports often omit real expiry; some clients drop exp=0 cookies.
func normalizeNetscapeExpiry(content string) string {
	lines := strings.Split(content, "\n")
	exp := fmt.Sprintf("%d", time.Now().Add(365*24*time.Hour).Unix())
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		if parts[4] == "0" || parts[4] == "" {
			parts[4] = exp
			lines[i] = strings.Join(parts, "\t")
		}
	}
	return strings.Join(lines, "\n")
}

// sanitizeNetscapeCookieValues strips bytes Go's net/http refuses in Cookie.Value
// (backslash etc.), which otherwise get dropped and can break Instagram cookies.
func sanitizeNetscapeCookieValues(content string) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			continue
		}
		parts[6] = strings.Map(func(r rune) rune {
			switch r {
			case '\\', '"', ',', ';', ' ', '\t', '\r', '\n':
				return -1
			default:
				if r < 0x20 {
					return -1
				}
				return r
			}
		}, parts[6])
		lines[i] = strings.Join(parts, "\t")
	}
	return strings.Join(lines, "\n")
}

func installIGCookieContent(content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return fmt.Errorf("contenuto vuoto")
	}
	if len(content) > igMaxCookieBytes {
		return fmt.Errorf("file troppo grande (max 2MB)")
	}
	if !netscapeHasSessionID(content) {
		return fmt.Errorf("manca sessionid nel file")
	}
	content = normalizeNetscapeExpiry(content)
	content = sanitizeNetscapeCookieValues(content)
	if err := os.MkdirAll(filepath.Dir(igCookiePath), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	tmp := igCookiePath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := os.Rename(tmp, igCookiePath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	_ = os.Chmod(igCookiePath, 0o600)
	invalidateIGSessionProbeCache()
	return nil
}

func confirmIGCookiesInstalled(bot *gotgbot.Bot, chatID int64) {
	st := probeIGSessionCached(true)
	var text string
	switch {
	case st.Alive:
		text = "✅ Cookie Instagram caricati e <b>sessione valida</b>.\n" +
			"File: <code>" + igCookiePath + "</code>\n" +
			"Cache ricaricata: il bot userà subito i nuovi cookie.\n" +
			"Messaggio cookie eliminato dalla chat."
	case !st.HasSessionID:
		text = "⚠️ Cookie scritti ma <b>manca sessionid</b>.\n" +
			"File: <code>" + igCookiePath + "</code>\n" +
			"Riesporta dopo un login completo e reinviameli.\n" +
			"Messaggio cookie eliminato dalla chat."
	case st.ProbeError != "":
		text = "⚠️ Cookie scritti ma non riesco a verificare la sessione (" + htmlEscape(st.ProbeError) + ").\n" +
			"File: <code>" + igCookiePath + "</code>\n" +
			"sessionid nel file: sì — riprova un download o /igcookies → Stato.\n" +
			"Messaggio cookie eliminato dalla chat."
	default:
		text = "⚠️ Cookie scritti ma Instagram <b>non accetta</b> la sessione.\n" +
			"Motivo: " + htmlEscape(st.Detail) + "\n" +
			"File: <code>" + igCookiePath + "</code>\n" +
			"sessionid nel file: sì, ma Instagram non ha accettato la sessione.\n" +
			"Rifai login nell’app, esporta di nuovo e invia come testo.\n" +
			"Messaggio cookie eliminato dalla chat."
	}
	_, err := bot.SendMessage(chatID, text, &gotgbot.SendMessageOpts{
		ParseMode: gotgbot.ParseModeHTML,
	})
	if err != nil {
		logger.L.Warnf("ig cookie confirm message failed: %v", err)
	}
}

// downloadTelegramDocument fetches a document from Telegram.
// With a local Bot API, getFile often returns a filesystem path (absolute or relative
// under /var/lib/telegram-bot-api). Prefer reading that file; HTTP /file/bot… 404s locally.
func downloadTelegramDocument(bot *gotgbot.Bot, fileID string) ([]byte, error) {
	f, err := bot.GetFile(fileID, nil)
	if err != nil {
		return nil, fmt.Errorf("getFile: %w", err)
	}
	if f.FilePath == "" {
		return nil, fmt.Errorf("file path vuoto")
	}

	candidates := localBotAPIFileCandidates(f.FilePath)
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		// Best-effort: remove the upload from local Bot API storage after reading.
		_ = os.Remove(path)
		return data, nil
	}

	url := f.URL(bot, nil)
	resp, err := http.Get(url) //nolint:gosec // Telegram Bot API file URL
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download status %d (prova a incollare i cookie come testo)", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, igMaxCookieBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(data) > igMaxCookieBytes {
		return nil, fmt.Errorf("file troppo grande (max 2MB)")
	}
	return data, nil
}

func localBotAPIFileCandidates(filePath string) []string {
	filePath = strings.TrimSpace(filePath)
	if filePath == "" {
		return nil
	}
	var out []string
	if strings.HasPrefix(filePath, "/") {
		out = append(out, filePath)
	}
	// Local Bot API relative paths: "<token>/documents/file_X"
	joined := filepath.Join("/var/lib/telegram-bot-api", filePath)
	out = append(out, joined)
	// Some builds strip the leading slash inconsistently.
	if strings.HasPrefix(filePath, "var/lib/telegram-bot-api/") {
		out = append(out, "/"+filePath)
	}
	return out
}

func saveIGCookieDocument(bot *gotgbot.Bot, doc *gotgbot.Document) error {
	if doc.FileSize > igMaxCookieBytes {
		return fmt.Errorf("file troppo grande (max 2MB)")
	}
	data, err := downloadTelegramDocument(bot, doc.FileId)
	if err != nil {
		return err
	}
	return installIGCookieContent(string(data))
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
	if msg == nil || bot == nil {
		return
	}
	if _, err := msg.Delete(bot, nil); err != nil {
		logger.L.Warnf("failed to delete sensitive IG message %d: %v", msg.MessageId, err)
	}
}
