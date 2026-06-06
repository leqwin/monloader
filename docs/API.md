# Authentication and REST API

## UI password

Off by default. Enable it in the settings or by command line : 

```bash
# On the host:
./monloader -hash-password 'your-password'
# Or via Docker:
docker exec -it monloader monloader -hash-password 'your-password'
```

Paste the result into `auth.password_hash` and set `auth.enable_password = true` in TOML.  
When enabled, the web UI is gated by a session cookie (lifetime `auth.session_lifetime_days`, default 7); removing the
password clears every session.

## API bearer token

The downloader exposes its own JSON API under `/api/v1/`. It is off-gate by
default; generate a token in **Settings -> Authentication**. When set, every endpoint except `/health`,
`/api/v1/openapi.json`, and `/api/v1/docs` requires
`Authorization: Bearer <token>`.

```bash
curl -H "Authorization: Bearer <token>" \
     -X POST http://localhost:8081/api/v1/queue \
     -d '{"url":"https://danbooru.donmai.us/posts/xxx"}'
```

## The API

The API documentation can be found in-app : 

- HTML reference: `/api/v1/docs` (linked in the footer).
- OpenAPI spec: `/api/v1/openapi.json`.