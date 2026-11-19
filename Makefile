BIN    := df17-decoder
CONFIG ?= config/config.yaml
# npm steps run only once the UI is scaffolded (web/package.json exists).
HAS_UI := $(wildcard web/package.json)

.PHONY: web build test lint run busy demo

web:
ifneq ($(HAS_UI),)
	cd web && npm ci && npm run build
else
	@echo "web/package.json not found, keeping the placeholder web/dist"
endif

build: web
	go build -o $(BIN) ./cmd/df17-decoder

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

# Synthetic 10-minute capture with 150 moving aircraft (Mode-S-only and one 7700 included).
busy:
	go run ./cmd/replaygen -o testdata/busy.txt

demo: build busy
	./$(BIN) -config config/demo.yaml
