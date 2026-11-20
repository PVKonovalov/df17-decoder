# Multi-stage build: web UI (Node), static Go binary with the UI embedded, distroless runtime.
#   make docker   (passes VERSION/COMMIT from git; .git is not in the build context)
#   docker run -d -p 8080:8080 -v $PWD/config/config.yaml:/etc/df17-decoder/config.yaml:ro \
#     -v df17-state:/var/lib/df17-decoder df17-decoder

FROM --platform=$BUILDPLATFORM node:26-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist/app web/dist/app
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/PVKonovalov/df17-decoder/internal/version.Version=${VERSION} -X github.com/PVKonovalov/df17-decoder/internal/version.Commit=${COMMIT}" \
    -o /out/df17-decoder ./cmd/df17-decoder \
 && mkdir -p /out/state

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/df17-decoder /usr/local/bin/df17-decoder
# Writable by nonroot, for coverage.file: /var/lib/df17-decoder/coverage.json and history.file:
# /var/lib/df17-decoder/history.db (mount a volume to keep them).
COPY --from=build --chown=nonroot:nonroot /out/state /var/lib/df17-decoder
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/df17-decoder"]
CMD ["-config", "/etc/df17-decoder/config.yaml"]
