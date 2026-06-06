# Sites and credentials

monloader does not parse any booru itself, gallery-dl does. monloader keeps a
set of curated profiles that sharpen the mapping for known sites, and falls back
to a generic profile for anything else gallery-dl supports.

## What is supported

- **Curated profiles** ship for 50-plus sites (booru and manga/comic),
  keyed by gallery-dl category. A profile carries the booru family (which
  decides rating and tag semantics), a post-URL template, the auth kind, and a
  representative example URL for the test probe.
- **The generic fallback** handles any other gallery-dl category: per-category
  tags by name where they match a monbooru category, a tolerant rating, and the
  post URL gallery-dl reports. So a site works the day gallery-dl supports it; a
  profile only sharpens the result.

## Auth kinds

The `login` column on the Settings page reflects each profile's auth kind. A
marker flags a site that needs a credential it does not yet have.

| Auth kind | Meaning | Examples |
|---|---|---|
| `none` | Reads without credentials | most boorus, most manga sites |
| `api_optional` | Works unauthenticated at low rates; a key raises rate limits | danbooru |
| `api_required` | Refuses to read without a key | gelbooru, rule34 |
| `cookies` | Needs a logged-in browser session exported to a cookies file | sankaku,  exhentai |

## Per-site setup

For each site you use, the Settings page exposes:

- **Credential fields** appropriate to the family: an api key with a user id
  (gelbooru family) or a username (danbooru / e621 family), or a cookies path.
  Secrets are write-only - the page shows whether a value is set, never the
  value itself.
- **A target gallery** dropdown, populated from monbooru's
  `GET /api/v1/galleries`. This is the per-source gallery the site's pushes land
  in; leave it empty to use the configured default. 
- **Test** runs a live probe: `gallery-dl -j --range 1-1` against the profile's
  example URL with the configured credentials, reporting `ok`,
  `auth_required`, `blocked`, or `failed` in that row, with gallery-dl's error
  on hover. A site still missing a required credential reads "needs cookies" /
  "needs api key"; a Cloudflare or captcha wall reads "blocked".
- **Reset** drops the credential block, reverting the site to its profile
  defaults.

Saving a site rewrites the managed `gallery-dl.json` from the config; that file
is never hand-edited.

## Cookies files

For a `cookies` site, export your logged-in session as a `cookies.txt` and place it
under the cookies directory (`/config/cookies` by default). Set its path in the
site's Settings row, or in the `[[sites]]` block:

```toml
[[sites]]
name    = "sankaku"
cookies = "/config/cookies/sankaku.txt"
gallery = "default"
```

## Equivalent TOML

The Settings rows write `[[sites]]` blocks. You can edit them directly instead:

```toml
[[sites]]
name    = "gelbooru"   # gallery-dl category
api_key = ""
user_id = ""
gallery = "art"        # per-source target; empty = default_gallery

[[sites]]
...
```

`name` is the gallery-dl category. How a site's tags and rating are mapped, and
how to add a profile for a new one, is in [MAPPING.md](MAPPING.md).
