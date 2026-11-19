BIN    := df17-decoder
CONFIG ?= config/config.yaml
# npm steps run only once the UI is scaffolded (web/package.json exists).
HAS_UI := $(wildcard web/package.json)

# Version from the nearest git tag, e.g. v1.0.0, v1.0.0-3-g4a2b4d4 or v1.0.0-dirty.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
VERPKG  := github.com/PVKonovalov/df17-decoder/internal/version
LDFLAGS := -s -w -X $(VERPKG).Version=$(VERSION) -X $(VERPKG).Commit=$(COMMIT)
# Static release binaries: linux/arm64 (Raspberry Pi 3/4/5, 64-bit OS), linux/arm (32-bit Raspberry Pi OS), linux/amd64.
PLATFORMS := linux/arm64 linux/arm linux/amd64
IMAGE   ?= df17-decoder

.PHONY: web build test lint run busy demo dist docker

web:
ifneq ($(HAS_UI),)
	cd web && npm ci && npm run build
else
	@echo "web/package.json not found, keeping the placeholder web/dist"
endif

build: web
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/df17-decoder

test:
	go test -race ./...
ifneq ($(HAS_UI),)
	cd web && npm test
endif

lint:
	go vet ./...
	@test -z "$$(gofmt -l .)" || { gofmt -l .; exit 1; }
ifneq ($(HAS_UI),)
	cd web && npm run lint && npm run typecheck
endif

run: build
	./$(BIN) -config $(CONFIG)

# Synthetic 10-minute capture with 150 aircraft around the receiver in config/demo.yaml.
busy:
	go run ./cmd/replaygen -config config/demo.yaml -o testdata/busy.txt

demo: build busy
	./$(BIN) -config config/demo.yaml

# dist/df17-decoder-<version>-<os>-<arch>: static binaries with the UI embedded (GOARM=7 for linux/arm).
dist: web
	@mkdir -p dist
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; out=dist/$(BIN)-$(VERSION)-$$os-$$arch; \
		echo "$$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch GOARM=7 go build -trimpath -ldflags "$(LDFLAGS)" -o $$out ./cmd/df17-decoder || exit 1; \
	done

# Container image tagged with the version and latest; .git is not in the build context, so the version is passed in.
docker:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .
