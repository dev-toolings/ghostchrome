# Ubuntu + Google Chrome + ghostchrome, built from this checkout.
#
# Build from the repository root:
#   docker build -f tools/deploy/chrome/Dockerfile -t ghostchrome .
#
# Modes (see tools/deploy/chrome/README.md):
#   docker run -i --rm --shm-size=1g ghostchrome mcp          # MCP over stdio
#   docker run --rm --shm-size=1g ghostchrome preview <url>   # one CLI command
#   docker run -d --shm-size=1g -p 127.0.0.1:9222:9223 ghostchrome cdp
#                                                             # CDP for the host
#
# Google Chrome ships Linux builds for amd64 only, so the image is amd64.

FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/ghostchrome ./cmd/ghostchrome

FROM ubuntu:26.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl gnupg socat tini \
      fonts-liberation fonts-noto-color-emoji \
 && curl -fsSL https://dl.google.com/linux/linux_signing_key.pub \
      | gpg --dearmor -o /usr/share/keyrings/google-chrome.gpg \
 && echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome.gpg] https://dl.google.com/linux/chrome/deb/ stable main" \
      > /etc/apt/sources.list.d/google-chrome.list \
 && apt-get update \
 && apt-get install -y --no-install-recommends google-chrome-stable \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/ghostchrome /usr/local/bin/ghostchrome
COPY tools/deploy/chrome/entrypoint.sh /usr/local/bin/ghostchrome-entrypoint
COPY tools/deploy/chrome/healthcheck.sh /usr/local/bin/ghostchrome-healthcheck

# Chrome's own sandbox needs user namespaces the default Docker seccomp
# profile denies; the container is the isolation boundary instead.
ENV GHOSTCHROME_NO_SANDBOX=1
RUN useradd --create-home --uid 10001 ghost
USER ghost
WORKDIR /home/ghost

EXPOSE 9223
# Mode-aware: cdp checks Chrome's DevTools endpoint, the other modes check the
# ghostchrome binary. Override it whenever you replace the entrypoint.
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=3 \
  CMD ["/usr/local/bin/ghostchrome-healthcheck"]
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/ghostchrome-entrypoint"]
CMD ["--help"]
