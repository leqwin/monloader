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

## API tokens

The downloader exposes its own JSON API under `/api/v1/`. It requires a
bearer token: the API is disabled until you create at least one in
**Settings -> Authentication**. Tokens are named,
individually revocable, and scoped to `read`
(queue, sites) and/or `write` (enqueue, manage jobs) via each token's
**config** dialog; a token missing the scope gets `403 insufficient_scope`.
`/health`, `/api/v1/openapi.json`, and `/api/v1/docs` stay open.

```bash
curl -H "Authorization: Bearer <token>" \
     -X POST http://localhost:8081/api/v1/queue \
     -d '{"url":"https://danbooru.donmai.us/posts/xxx"}'
```

## Pairing

In monloader click **connect to monbooru** under
**Settings -> monbooru** (monbooru's operator approves there), and in the
monsender browser extension click **connect to monloader** (you approve it here
under **Settings -> monsender**). Each pairing provisions a scoped token
automatically; remove it on either side to re-pair.

## The API

The API documentation can be found in-app : 

- HTML reference: `/api/v1/docs` (linked in the footer).
- OpenAPI spec: `/api/v1/openapi.json`.