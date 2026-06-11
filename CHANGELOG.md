# Changelog

## [v1.1.0] - 2026-06-11
### Added
- Topbar and landing-page links to your monbooru instance.
- The footer connection indicator shows the connected monbooru's version.
- "Get all" action fetches a capped search to the end, continuing automatically.
- `monloader healthcheck` subcommand, so the container healthcheck no longer needs curl.
- API: queue jobs expose their continuation-series root and a continue-all endpoint.

### Changed
- Long job item lists fold into a "+N more" toggle.
- A capped search and its continuation windows now show as one queue row.
- Retry and force-download actions appear only when they would do something.
- Destructive actions confirm through an in-page dialog instead of the browser popup.
- UI nav, contrast, and settings cards restyled to match monbooru.
- Container image rebased on Debian 13 (Python 3.13).

### Fixed
- Retrying or re-adding a URL that failed to import re-downloads it instead of being skipped.
- Very long URLs or sources are trimmed to monbooru's limits instead of failing the import.
- Queue "view" links no longer open the wrong image after a deletion in monbooru.
- The politeness delay now throttles each file download, so a multi-file fetch no longer bursts.

## [v1.0.1] - 2026-06-07
### Added
- Import a manga or comic title as one CBZ per chapter.

### Changed
- Queue and history items now show as compact, aligned one-line rows.

### Fixed
- Forum threads and dispatcher pages with off-site images now download instead of silently matching nothing.
- Media monbooru cannot ingest (audio, SVG, AVIF) is skipped or declined instead of failing the job.

## [v1.0.0] - 2026-06-06
### Added
- Initial release.
