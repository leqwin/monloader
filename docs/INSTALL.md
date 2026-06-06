# Installing

monloader runs as a container next to monbooru. It needs a monbooru instance
to push into and an API token for it; nothing else is required to start.

## Quick start (alongside monbooru)

monloader ships as a commented service in monbooru's
`docker/docker-compose.yml`, so enabling it there puts the two on one network
with no extra wiring.

1. In monbooru, open **Settings -> Authentication** and generate an API token.
2. Uncomment the `monloader` service in monbooru's compose and start it:
   ```bash
   docker compose up -d monloader
   ```
3. Open `http://localhost:8081`, go to **Settings -> monbooru**, paste the
   token, pick a default gallery, and click **test connection**. 

On first run monloader writes `/config/monloader.toml` with defaults and a
managed `gallery-dl.json` alongside it. The token and most other settings are
editable from the Settings page. You can use `MONBOORU_API_TOKEN=<token>` as well.

## Volume layout

| Mount | Purpose |
|---|---|
| `/config` | `monloader.toml`, the managed `gallery-dl.json`, the gallery-dl download-archive, and cookies files. |

## Environment variables

All override the TOML config. Pattern: `MONLOADER_{SECTION}_{KEY}`.

| Variable | Overrides | Type |
|---|---|---|
| `MONLOADER_SERVER_BIND_ADDRESS` | `server.bind_address` | string |
| `MONLOADER_SERVER_BASE_URL` | `server.base_url` | string |
| `MONLOADER_MONBOORU_API_URL` | `monbooru.api_url` | string |
| `MONLOADER_MONBOORU_API_TOKEN` | `monbooru.api_token` | string |
| `MONLOADER_MONBOORU_WEB_URL` | `monbooru.web_url` | string |
| `MONLOADER_MONBOORU_DEFAULT_GALLERY` | `monbooru.default_gallery` | string |
| `MONLOADER_DOWNLOADER_CONCURRENCY` | `downloader.concurrency` | int |
| `MONLOADER_DOWNLOADER_MAX_ITEMS_PER_JOB` | `downloader.max_items_per_job` | int |
| `MONLOADER_DOWNLOADER_DEFAULT_FOLDER` | `downloader.default_folder` | string |
| `MONLOADER_GALLERYDL_BINARY_PATH` | `gallerydl.binary_path` | string |
| `MONLOADER_GALLERYDL_CONFIG_PATH` | `gallerydl.config_path` | string |
| `MONLOADER_GALLERYDL_ARCHIVE_PATH` | `gallerydl.archive_path` | string |
| `MONLOADER_GALLERYDL_COOKIES_DIR` | `gallerydl.cookies_dir` | string |
| `MONLOADER_GALLERYDL_SLEEP_REQUEST` | `gallerydl.sleep_request` | float |
| `MONLOADER_AUTH_ENABLE_PASSWORD` | `auth.enable_password` | bool |
| `MONLOADER_AUTH_PASSWORD_HASH` | `auth.password_hash` | string |
| `MONLOADER_AUTH_API_TOKEN` | `auth.api_token` (the downloader's own bearer token) | string |
| `MONLOADER_LOG_LEVEL` | `log.level` | `warn` / `info` / `debug` |

## Custom CSS

Set `custom_css` in `[server]` to a path and monloader serves it at
`/custom.css`, linked after the bundled `main.css`, so a `:root` block there
wins the cascade.

## Logo and title

`name` and `logo` in `[server]` rebrand the UI. `name` replaces the wordmark,
every page `<title>`, and the login heading; the CSS uppercases the wordmark,
so `myloader` renders as `MYLOADER`. Empty falls back to `monloader`. `logo`
is a path to an image served at `/custom.logo` and used for both the favicon
and the logo; empty falls back to the bundled assets.

## Log levels

`log.level`:

- `warn` (default) - warnings, errors, and explicit mutations (logins,
  settings saves).
- `info` - adds one line per non-noisy HTTP request and the startup banner
  (gallery-dl version, extractor count, work dir).
- `debug` - adds the 2-second queue poll, the connectivity-light check, and
  `/health` hits.
