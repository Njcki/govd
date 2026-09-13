<h1 align="center">govd</h1>
<p align="center">
  <a href="https://t.me/govd_bot">
    <img alt="govd" title="govd" src="https://i.imgur.com/Vx8Psjn.png" width="450">
  </a>
</p>

<p align="center">
  extremely lightweight downloader, inside a telegram bot.
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/github/license/govdbot/govd?style=flat-square" alt="license"></a>
  <a href="https://github.com/govdbot/govd/stargazers"><img src="https://img.shields.io/github/stars/govdbot/govd?style=flat-square" alt="stars"></a>
  <a href="https://github.com/govdbot/govd/forks"><img src="https://img.shields.io/github/forks/govdbot/govd?style=flat-square" alt="forks"></a>
  <img src="https://img.shields.io/badge/docker-ready-blue?style=flat-square" alt="docker">
  <a href="https://t.me/govd_bot"><img src="https://img.shields.io/badge/telegram-@govd__bot-2CA5E0?style=flat-square&logo=telegram" alt="telegram"></a>
  <a href="https://tgbotmau.quoi.dev/?bot=govd_bot" target="_blank"><img alt="@govd_bot MAU" title="@govd_bot MAU" src="https://tgbotmau.quoi.dev/api/bot/govd_bot/mau/badge?style=flat-square"></a>
</p>

## features

* vast number of extractors supported
* extremely lightweight
    * minimal memory usage (~80MB)
    * minimal disk usage (~150MB)
* easy to deploy with docker
* highly configurable and extensible
* supports self hosted telegram bot api
* supports authentication for extractors
* available in private chats, groups and inline mode
* translation ready (i18n)

## getting started

everything you need to get started with the project can be found in the [wiki](https://github.com/govdbot/govd/wiki).



## changes in this fork

this fork is based on [govdbot/govd](https://github.com/govdbot/govd). general setup still follows the [upstream wiki](https://github.com/govdbot/govd/wiki); the notes below cover only what this fork adds.

### what's different

* **max video quality** in `/settings` — Best / 2160p / 1080p / 720p / 480p (default Best = no cap). applies to every extractor that exposes multiple video formats. already-cached downloads keep their previous quality until you re-download or clear the media cache.
* **captions** setting is available in private chats as well as groups.
* **instagram** uses real session cookies from `private/cookies/` (never commit them). fallback chain after GraphQL: authenticated **media info API** (photos + mixed carousels) → **yt-dlp** → embed → iGram. clearer `ErrorInstagramCookies` when `sessionid` is missing/expired.
* **youtube** tries configured Invidious instances first, then falls back to **yt-dlp** (optional `private/cookies/youtube.txt`).
* **inline mode** shows a localized **source** button on the processing placeholder (useful when BotFather Inline Feedback is off and the placeholder never updates).

### using the new settings

1. start a chat with your bot (or open a group where it is admin).
2. send `/settings`.
3. open **max video quality** and pick Best / 2160p / 1080p / 720p / 480p.
4. optional: enable **captions** in a private chat the same way (no longer groups-only).

change the quality before sending a new link. if you already downloaded the same url, clear that media from the bot database/cache or expect the old file until it is fetched again.

### cookies (instagram / x / tiktok / …)

authenticated extractors read netscape-format cookie files under `private/cookies/` (for example `instagram.txt`, `twitter.txt`, `tiktok.txt`). those paths are gitignored — put cookies only on the host that runs the bot.

### Instagram cookies (admins)

Admins can update Instagram cookies in a private chat with the bot:

1. Prefer pasting the Netscape cookie file **as plain text** in DM (works with cloud and local Bot API; no file download).
2. Or send a document named `instagram.txt` / `*instagram*.txt`.
3. Commands: `/igcookies` or `/igadmin` (also a start-menu button for admins).
4. After a successful install the bot deletes the cookie message from the chat.

If you run a **local** Telegram Bot API (`BOT_API_URL`), mount the Bot API data dir into the bot container read-only, e.g. `./telegram-bot-api-data:/var/lib/telegram-bot-api:ro`, so document uploads can be read from disk. Text paste does not need that mount.


* export cookies from a real logged-in browser session (browser extension or your usual workflow).
* do **not** commit cookie files, `.env`, or `private/config.yaml`.
* after updating cookies, restart the bot container so clients reload them.

instagram cookies must include **`sessionid`** (HttpOnly). without it the bot refuses the install path / shows a clear cookie error instead of a cryptic hash.

instagram download order in this fork:

1. GraphQL (shared Windows Chrome 152 User-Agent + matching client hints)
2. media info API `/api/v1/media/{id}/info/` with cookies — maps photo / video / carousel (including mixed albums); same shared User-Agent
3. yt-dlp with the same cookie file (video-oriented; its own UA)
4. embed / iGram last-resort paths

HTTP User-Agent is centralized as `networking.DefaultUserAgent` (Windows Chrome 152). previous Android/Mac/Chrome 124 strings are kept in comments next to that constant for rollback.

prefer a **dedicated secondary** Instagram account for the VPS. reusing a primary account from a datacenter IP can trigger Meta automation checkpoints.

### admin error alerts

set `ADMINS` in the host `.env` to one or more numeric Telegram user IDs (comma-separated). those accounts get a private DM with error details when Instagram auth fails or an unexpected download error is logged. end users only see a generic “error reported” message. do not commit real admin IDs.

### building this fork

default image (includes yt-dlp, CGO-free build when libheif packages break):

```bash
docker build -t govd:fork .
```

alternative that layers a rebuilt binary + yt-dlp onto the official runtime image:

```bash
docker build -f Dockerfile.instagram-fix -t govd:fork .
```

then run with your usual `docker compose` / env from the [wiki](https://github.com/govdbot/govd/wiki) (`BOT_TOKEN`, database, optional self-hosted Bot API, etc.). point the compose `image` (or build context) at this fork instead of `govdbot/govd:main`.

on first start, goose applies migration `00011_add_max_video_height_setting.sql` (`settings.max_video_height`, default `0`).

### inline feedback

for full inline downloads (placeholder replaced by media), enable **Inline Feedback** in [@BotFather](https://t.me/BotFather) for your bot. without it, users still get the source button on the processing message.

## migrating from v1

if you are migrating from govd v1 to v2, refer to the [migration tool](https://github.com/govdbot/migrate).

## community

- [official bot](https://t.me/govd_bot)
- [support chat](https://t.me/govdsupport)

## license

this project is licensed under the [mit license](LICENSE).
