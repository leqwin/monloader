# Stage 1: build the static Go binary.
FROM golang:1.25-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN VERSION=$(cat VERSION.md 2>/dev/null | tr -d '[:space:]') && \
    REPO=$(cat REPOSITORY.md 2>/dev/null | tr -d '[:space:]') && \
    CGO_ENABLED=0 go build \
      -ldflags="-s -w -X 'github.com/leqwin/monloader/internal/web.Version=${VERSION}' -X 'github.com/leqwin/monloader/internal/web.RepoURL=${REPO}'" \
      -o /monloader ./cmd/monloader

# Stage 2: runtime with Python and a pinned gallery-dl. 
FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
    && rm -rf /var/lib/apt/lists/*
RUN pip install --no-cache-dir gallery-dl==1.32.1

COPY --from=builder /monloader /usr/local/bin/monloader

# Run as an unprivileged user;
RUN useradd --uid 1000 --user-group --create-home --home-dir /home/monloader --shell /usr/sbin/nologin monloader
USER monloader
ENV HOME=/home/monloader

EXPOSE 8081
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl --fail --silent --max-time 5 http://localhost:8081/health || exit 1
ENTRYPOINT ["/usr/local/bin/monloader", "-config", "/config/monloader.toml"]
