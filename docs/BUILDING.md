# Building from source

monloader is pure Go with no CGO, so the binary is static
and builds without system libraries.

```bash
make build      # go build with the version injected from VERSION.md
# or directly:
go build -o monloader ./cmd/monloader

./monloader -config /path/to/monloader.toml
```

`make build` injects the version and repository URL via `-ldflags` (read from
`VERSION.md` and `REPOSITORY.md`), which feed the footer and `/health`. A plain
`go build` reports `dev`.

## CLI flags

- `-config` - path to the TOML config file (default `./monloader.toml`).
- `-hash-password '...'` - print a bcrypt hash for the UI password and exit.
- `-version` - print the version and exit.

## gallery-dl

The container image bundles Python and a pinned gallery-dl. Outside the
container you supply it yourself.

If it is not on `PATH`, set `gallerydl.binary_path` to its location. A missing
binary is not fatal at startup - the UI and API still run, the bundled version
shows as unavailable, and downloads fail with a clear error until it is
installed.