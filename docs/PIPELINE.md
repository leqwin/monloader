# How a download flows

A queued URL ("job") expands into one or more items, each downloaded, mapped,
and pushed independently. This page covers that pipeline, the in-memory queue
behind it, and the outcome each item ends up with.

```
  add bar / POST /api/v1/queue
        |
        v
  in-memory queue ---> worker ---> gallery-dl (resolve + download to /work)
                                        |
                                  map metadata (tags, rating, source, pool)
                                        |
        multipart POST /api/v1/images?gallery=<name> --> monbooru
```

## Per-item outcomes

Every item ends with one of five outcomes, never silently dropped:

| outcome | meaning |
|---|---|
| `created` | new image accepted by monbooru (HTTP 201) |
| `duplicate` | monbooru already had this sha256 (HTTP 200, alias) |
| `skipped_archive` | gallery-dl's archive already had this post; not fetched |
| `skipped_unsupported` | monbooru cannot ingest this file type; not pushed |
| `failed` | something went wrong; carries an `error_code` |

A `failed` item carries one of these stable codes :

| error_code | when |
|---|---|
| `unsupported_url` | gallery-dl matched no extractor for the URL, and it is not itself a direct link to a media file monbooru can ingest |
| `auth_required` | the site needs credentials (a 401/403 with a missing-auth message) |
| `blocked` | a bot-protection wall (Cloudflare / captcha challenge), kept distinct from `auth_required` so its 403 is not read as a missing credential |
| `rate_limited` | the site returned 429 / a rate-limit error |
| `network_unreachable` | gallery-dl could not resolve or reach the host (DNS failure, network unreachable); a refused/dropped connection stays `download_failed` |
| `download_failed` | any other non-zero gallery-dl exit (a reached host erroring, HTTP 4xx/5xx, a refused/dropped connection) |
| `mapping_failed` | metadata present but no usable file or URL could be built |
| `file_too_large` | monbooru rejected the upload for size |
| `monbooru_unreachable` | the push got no HTTP response (connect / timeout) |
| `monbooru_rejected` | monbooru returned a 4xx/5xx other than the duplicate 200 |
| `canceled` | the job was canceled while the item was in flight |

The job's `summary` aggregates the counts:
`{ created, duplicate, skipped, failed, total }`. A single-post enqueue that was
already saved resolves to a summary like `{ created: 0, duplicate: 1, ... }.

## Pools and manga

A booru pool, or a manga/comic gallery's pages, is a multi-page work, handled
by kind:

- **Booru pool** - each page is pushed as its own image under a shared
  `collection` label (the pool name) and `collection_order` (the page number),
  keeping each page's own metadata. A pool is one work you asked for as a unit,
  so it is exempt from `max_items_per_job` and comes down whole in one job (an
  over-cap resolve re-resolves uncapped).
- **Manga/comic gallery** - the pages are bundled, ordered, into a single
  `.cbz` and pushed as one file, which monbooru ingests as a manga archive for
  its reader. The bundle's tags are the union across the pages, its rating is
  the strictest seen, and the gallery name names the `.cbz` file. Like a pool, a
  gallery is exempt from `max_items_per_job` and fetched whole,
  and the job fails rather than pushing a truncated book if any page is missing.

Manga/comic sites are flagged in their profile; see [MAPPING.md](MAPPING.md) for
the per-site details.
