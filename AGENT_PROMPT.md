# Task: Mode S / ADS-B decoder service in Go (`df17-decoder`)

## Context

I run a home 1090 MHz receiver:

- RTL-SDR Blog V4 dongle (R828D tuner), passed through USB to an Ubuntu 22.04 VM on ESXi.
- `dump1090-fa` runs on that VM in network mode, so it does the demodulation and serves frames over TCP:
  - **30002**: raw AVR text, one frame per line: `*8D4840D6202CC371C32CE0576098;` (14 hex chars = 56-bit frame, 28 hex chars = 112-bit frame)
  - **30005**: Beast binary (same frames, plus a 12 MHz timestamp and signal level)

dump1090 already decodes everything, but I want **my own decoder written from scratch in Go**, to learn the protocol and to own the data pipeline. **Do not use dump1090's decoded outputs (SBS on 30003, JSON) and do not use third-party ADS-B decoding libraries.** Consume raw frames only.

The repo is this directory. Module path: `github.com/PVKonovalov/df17-decoder`.

## Goal

A long-running Go service that:

1. Connects to dump1090's raw output over TCP, and reconnects automatically.
2. Validates and decodes Mode S frames (DF17/18 ADS-B first, then the other DFs).
3. Keeps a live table of aircraft state.
4. Exposes that state over an HTTP/JSON API.

## Technical requirements

- Go 1.22+ with the **standard library only**: `net/http` with the 1.22 routing patterns, `log/slog`, and `encoding/json`. The one allowed exception is `gopkg.in/yaml.v3`, for the config file. Ask me before adding any other dependency.
- Graceful shutdown with `signal.NotifyContext`. Every goroutine must stop when the context is cancelled.
- **Configuration lives in a YAML file.** The service reads `-config path/to/config.yaml` (default `./config.yaml`). Commit a fully commented `config.example.yaml` with all keys and their defaults.
  - Precedence: built-in defaults < YAML file < the few CLI flags. Only `-config` and `-log-level` are flags; everything else is in YAML.
  - Validate on startup (lat/lon ranges, positive durations, parseable source URL) and exit with a clear error message on invalid config. Reject unknown keys (`yaml.Decoder.KnownFields(true)`) so typos don't fail silently.
  - Durations are written as Go duration strings (`60s`, `10s`). Add a small custom type that implements `yaml.Unmarshaler`.
  - Schema:
    ```yaml
    source:
      url: tcp://localhost:30002     # also beast://host:30005, file://testdata/capture.txt
      reconnectMin: 1s
      reconnectMax: 30s
      replaySpeed: 1.0               # file:// only; 0 = as fast as possible
    receiver:
      lat: XX.XX
      lon: XX.XX
      maxRangeNm: 300
    decoder:
      fixSingleBitErrors: false      # DF17 only
      cprPairMaxAge: 10s
    tracker:
      ttl: 60s
      knownAddressTTL: 60s
    api:
      listen: ":8080"
      streamInterval: 1s
    log:
      level: info                    # debug|info|warn|error
      format: text                   # text|json
    ```
- Suggested layout (adjust if you have a good reason):
  ```
  cmd/df17-decoder/main.go
  internal/modes/      # frame type, CRC, DF/TC decoding, Gillham, CPR, charset
  internal/source/     # AVR reader, Beast reader, file replay
  internal/tracker/    # aircraft state, expiry, ICAO table of known addresses
  internal/api/        # HTTP handlers
  internal/config/     # YAML loading, defaults, validation
  config.example.yaml
  testdata/
  ```
- `internal/modes` must be pure: no I/O and no global state. It takes `[]byte` plus a receive time and returns typed structs, so it can be tested with table-driven tests.

## Input sources

1. **AVR (port 30002)**: lines `*<hex>;`. Ignore empty or malformed lines, and count them in stats. Also accept the `@<12-hex-timestamp><hex>;` variant if it shows up.
2. **File replay** (`file://capture.txt`): the same AVR format, read from a file, for tests and offline development without the radio. Speed is set by `source.replaySpeed`.
3. **Beast (port 30005)**, later milestone: `0x1A` escape byte. Type `'1'` = Mode A/C (2 bytes), `'2'` = Mode S short (7 bytes), `'3'` = Mode S long (14 bytes). Each frame is preceded by a 6-byte timestamp and 1 byte of signal level. A literal `0x1A` inside the data is doubled. Expose the signal level as RSSI (dBFS) on the aircraft.

Reconnect with exponential backoff (1s → 30s max). Report connection state in `/api/stats`.

## Decoding

### Validation (CRC-24, generator `0xFFF409`)

- **DF17/18**: the CRC remainder over all 112 bits must be 0. Optionally fix single-bit errors on DF17 only (off by default, behind a flag).
- **DF11**: the remainder must be ≤ 0x7F (it's the interrogator code XORed with the parity). The ICAO address is in the clear in bits 9–32.
- **DF0/4/5/16/20/21**: address/parity. The CRC remainder *is* the ICAO address. Accept the frame only if that address is already in the tracker's known-address table, meaning we've seen it in a valid DF11/17/18 recently. Otherwise drop it and count it. This is the standard way to filter noise.

### DF17 / DF18 extended squitter (ME field, type code = first 5 bits)

| TC | Content |
|---|---|
| 1–4 | Identification: 8-char callsign using the 6-bit charset `#ABCDEFGHIJKLMNOPQRSTUVWXYZ##### ###############0123456789######`, plus emitter category |
| 5–8 | Surface position (lower priority; decode if time allows) |
| 9–18 | Airborne position + barometric altitude |
| 19 | Airborne velocity: subtypes 1/2 = ground speed from E-W/N-S components; subtypes 3/4 = airspeed + heading; vertical rate with sign and source |
| 20–22 | Airborne position + GNSS altitude |
| 28 | Aircraft status: emergency/priority state and squawk |
| 29 | Target state and status (selected altitude and heading): nice to have |
| 31 | Operational status: ADS-B version (0/1/2) |

Altitude (12-bit field): with Q-bit = 1 it's `25 × N − 1000` ft. With Q-bit = 0 it's a Gillham Gray code in 100 ft steps. Implement both.

### CPR position decoding

- **Global decode**: needs an even and an odd frame from the same aircraft, received less than 10 s apart. NZ = 15. Implement the NL(lat) function exactly (closed form or lookup table). Reject the result if the two frames fall in different NL zones. Use the most recent frame of the pair to produce the position.
- **Local decode**: from a single frame, relative to a reference. Use the aircraft's last known position if it's recent, otherwise the receiver position (valid within 180 nm).
- **Sanity checks**: reject positions farther than `receiver.maxRangeNm` from the receiver, and positions that imply an impossible speed compared to the aircraft's previous position. Count rejections.

### Non-ADS-B Mode S (important here: local GA traffic only sends these)

- **DF11** (all-call reply): ICAO address and capability. Feeds the known-address table.
- **DF4/20** (surveillance/Comm-B altitude reply): 13-bit AC field with M and Q bits → altitude.
- **DF5/21** (surveillance/Comm-B identity reply): 13-bit ID field → squawk (Gillham-ordered bits → 4 octal digits).
- **DF20/21 Comm-B**: if the MB field starts with `0x20` (BDS 2,0), decode the callsign. Leave the other BDS registers for later.
- **DF0/16**: altitude only.

## Aircraft state (tracker)

For each ICAO address, keep:

- `icao` (6 hex chars), `callsign`, `squawk`, `category`, `adsbVersion`
- `altBaro`, `altGnss`, `groundSpeed`, `track`, `ias`/`tas`, `heading`, `verticalRate`, `onGround`
- `lat`, `lon`, `posTime`, plus `distanceNm` and `bearing` from the receiver (haversine)
- `firstSeen`, `lastSeen`, `messages`, per-DF message counts, `rssi` (Beast only)
- `emergency` (from TC28, or squawk 7500/7600/7700)
- `country`: derived from the ICAO address block, using a small built-in table. At least Israel `738000–73FFFF` plus the common European blocks. This is a nice-to-have.
- `hasPosition` / `adsb` flags, so Mode-S-only aircraft are easy to tell apart

Expire aircraft after `tracker.ttl` with no messages. The tracker must be safe for concurrent use: one writer goroutine and many HTTP readers. Return copies or snapshots, never internal pointers.

## HTTP API

- `GET /api/aircraft`: JSON array of current aircraft. Optional query params: `?withPosition=true`, `?sort=distance|altitude|lastSeen`
- `GET /api/aircraft/{icao}`: one aircraft, or 404
- `GET /api/stats`: uptime, source connection status, total frames, frames per DF, CRC failures, address/parity drops, CPR rejections, malformed lines, current aircraft count, messages per second (1-minute window)
- `GET /api/stream`: Server-Sent Events, pushing an aircraft snapshot every 1 s
- `GET /healthz`: 200 if the source has delivered a frame within the last 30 s, otherwise 503

Use JSON field names in camelCase, and leave out fields that are unknown instead of sending zero values. Use pointers or `omitempty` as appropriate. A real altitude of 0 must stay distinguishable from "no altitude".

## Tests (required, not optional)

Table-driven unit tests in `internal/modes`, using these known vectors from *The 1090 MHz Riddle* (mode-s.org):

| Frame | Expected |
|---|---|
| `8D4840D6202CC371C32CE0576098` | DF17, ICAO `4840D6`, TC 4, callsign `KLM1023` |
| even `8D40621D58C382D690C8AC2863A7` + odd `8D40621D58C386435CC412692AD6` | ICAO `40621D`, altitude 38000 ft, global CPR (even frame is newest) → lat ≈ 52.2572, lon ≈ 3.9194 |
| `8D485020994409940838175B284F` | TC 19 subtype 1, ground speed ≈ 159.2 kt, track ≈ 182.88°, vertical rate −832 ft/min |
| `8DA05F219B06B6AF189400CBC33F` | TC 19 subtype 3, heading ≈ 243.98°, airspeed 375 kt (TAS), vertical rate −2304 ft/min |

Also include:
- CRC: valid frames give remainder 0, and flipping any single bit gives a non-zero remainder.
- Gillham altitude and squawk decoding, using known code/value pairs.
- NL function boundary values.
- CPR local decode against the same vector, using a nearby reference position.
- Tracker: expiry, the known-address table, rejection of an impossible position jump.
- An integration test that replays `testdata/capture.txt` through the full pipeline and checks for no panics, a non-zero aircraft count, and sane stats. I'll provide a real capture with `nc <vm-ip> 30002 > testdata/capture.txt`. Until then, generate a small synthetic one from the vectors above.

Run `go test -race ./...`. `go vet ./...` must be clean, and the code must be gofmt-formatted.

## Milestones

Work in this order. Each milestone should build, pass its tests, and be committed separately with a clear message:

1. Project skeleton, YAML config (with tests for defaults, overrides, validation errors and unknown keys), AVR TCP reader with reconnect, file replay, CRC, frame parsing, DF17 TC 1–4 (callsign). Log decoded callsigns.
2. Altitude (both encodings), global and local CPR, position sanity checks.
3. Velocity (TC 19, all subtypes), TC 28 and TC 31.
4. Non-ADS-B Mode S: DF11, DF0/4/5/16/20/21 with address/parity, Gillham squawk, BDS 2,0.
5. Tracker + HTTP API + SSE + stats.
6. Beast input with RSSI.
7. README: what it is, how to run it against dump1090, a sample curl output, and the architecture in a few lines.

## Working rules

- Read the relevant section of *The 1090 MHz Riddle* (https://mode-s.org/decode/) before implementing each decoder, and cite the section in a short comment above the function.
- Bit extraction: write one small, well-tested helper (for example, `bits(msg []byte, start, length int) uint64` with 1-based bit numbering to match the spec) and use it everywhere. Don't hand-roll shifts in each decoder.
- If you're unsure how to interpret a field, make the test fail clearly instead of guessing silently. Tell me what's ambiguous.
- Keep `main.go` thin. All logic goes in `internal/`.
- Don't push to a remote. Local commits only.
