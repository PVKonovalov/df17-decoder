# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Common requirements

- Before implementing anything, always first briefly describe what you'll be doing and ask for my consent.
- After implementation, write down in RELEASE.md file what was implemented in the format "Date: Implementation
  Description" (< 200 characters)
- When changing an API (a REST endpoint's path/params, or a request/response shape), find and check every caller of it —
  both backend (other handlers/services) and frontend — and update them too.
- Use Go 1.26
- Don't use a lot of code comments. For function describe arguments and results. The best comments is a code.
- use go fix ./...

## Project

Go project for a Mode S / ADS-B decoder (per README): DF17 decoding (callsign, altitude, CPR position, velocity), Mode S altitude/squawk for non-ADS-B aircraft, an aircraft state table, and a JSON API.

The full spec and milestone plan are in `AGENT_PROMPT.md` (backend) and `FRONTEND_PROMPT.md` (web UI). Consume raw frames only (AVR/Beast); no dump1090 decoded outputs, no third-party ADS-B libraries, stdlib only apart from `gopkg.in/yaml.v3`.

**Progress:** Milestone 1 is done (config, AVR TCP/file sources, CRC, DF17/18 TC 1–4). Milestones 2–7 are not started.

## Commands

```sh
go build ./...
go test -race ./...
go test ./internal/modes -run TestDecode_Identification   # single test
go vet ./... && gofmt -l . && go fix ./...
go run ./cmd/df17-decoder -config config.yaml -log-level debug
```

For offline runs, set `source.url: file://testdata/capture.txt` with `replaySpeed: 0` in a local `config.yaml`, which is gitignored. `testdata/capture.txt` is synthetic, built from the mode-s.org test vectors, and `internal/app` tests assert exact counts against it. Update those tests if you change the file.

## Architecture

Data flow: `source` → `chan source.Frame` → `app.Run` decode loop → (tracker/API in later milestones).

- `internal/config`: defaults < YAML (`KnownFields(true)`) < CLI flags (`-config`, `-log-level` only). `Load` validates and reports all errors together. `config.Duration` is a YAML duration string. `Source.Endpoint()` splits `source.url` into scheme and address. `config.example.yaml` must equal `Default()`; a test enforces this.
- `internal/modes`: pure, with no I/O and no global state. `Decode(msg, time, Options)` returns a `Message` and always sets DF, even on error, so callers can count drops. All bit access goes through `bits(msg, start, length)`, which uses 1-based, MSB-first numbering matching mode-s.org. `Remainder` is the CRC-24 syndrome: 0 for DF17/18, the IC for DF11, the ICAO address for address/parity DFs. Each decoder cites its mode-s.org section in a one-line comment.
- `internal/source`: `TCP` (reconnects with exponential backoff, closes the conn via `context.AfterFunc` on cancel) and `File` (replay paced by `@` 12 MHz timestamps; `*` lines are emitted immediately). Both share `readAVR` and atomic `Stats`. Beast is not implemented yet.
- `internal/app`: wiring and the decode loop. `cmd/df17-decoder/main.go` stays thin.

## Legacy support packages (unused by the service)

- `pkg/configuration` — loads config into a struct from a YAML file, then overrides fields from env. Precedence: YAML < dotenv file (`EnvFile`, default `.env`, missing file ignored) < process environment. Only fields tagged `env:"true"` are overridable. The env var name is `EnvVarPrefix` + the upper-cased `yaml` tag names of the enclosing structs and the field joined by `_` (e.g. `api.port` → `<PREFIX>API_PORT`). Supported kinds: string, bool (`true`/`1`/`y`), int/int32/int64, `[]string` (comma-separated). `ListEnv` lists all overridable variable names. `EnvVarPrefix`, `EnvFile`, and `ReadFromFile` are package-level vars — tests swap them and restore with `t.Cleanup`.
- `pkg/llog` — leveled logger (error/warn/info/debug/trace) wrapping stdlib `log`; global `llog.Logger`, default level info. Info/debug/trace/warn go to stdout, errors to stderr in ANSI red.

Both packages carry their own MIT `LICENSE` and a license header comment in each source file; keep that header on new files in these packages.
