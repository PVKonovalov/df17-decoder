# df17-decoder

A Mode S / ADS-B decoder written from scratch in Go. It reads raw 1090 MHz frames from
[dump1090](https://github.com/flightaware/dump1090) and decodes them itself. It does not use dump1090's decoded
outputs (SBS, JSON) or any third-party ADS-B library. Live aircraft state is served over an HTTP/JSON API and a
WebSocket, and shown in a built-in web UI with a map and a table.

What it decodes:

- **DF17/18 extended squitter:** callsign and emitter category, barometric and GNSS altitude, airborne position
  (global and local CPR), velocity (ground speed or airspeed with heading, vertical rate), emergency state and
  squawk, ADS-B version.
- **Non-ADS-B Mode S:**
  - DF11 all-call replies.
  - DF0/4/16/20 altitude and DF5/21 squawk, accepted via address/parity only for addresses already seen in a
    valid DF11/17/18 frame.
  - BDS 2,0 callsign in Comm-B replies.
- **Input:** AVR text (port 30002), Beast binary with signal level as RSSI (port 30005), or AVR file replay.

The backend uses the Go standard library plus `gopkg.in/yaml.v3` and `github.com/gorilla/websocket`, and requires
Go 1.26. Building the web UI also needs Node 26+ and npm. Without Node the binary still builds and serves a
placeholder page.

## Running against dump1090

dump1090-fa serves raw AVR frames on port 30002 and Beast frames on port 30005 when it runs with networking
enabled (`--net`). Copy the example config, then set the source and your receiver position:

```sh
cp config/config.example.yaml config/config.yaml
```

```yaml
source:
  url: beast://192.168.1.50:30005   # or tcp://192.168.1.50:30002 for AVR
receiver:
  lat: 20.19
  lon: 11.89
```

```sh
go run ./cmd/df17-decoder -config config/config.yaml
go run ./cmd/df17-decoder -config config/config.yaml -log-level debug   # override log.level
```

Without `-config` the service runs on built-in defaults (`tcp://localhost:30002`, API on `:8080`). Every key is
documented in [`config/config.example.yaml`](config/config.example.yaml). Unknown keys and invalid values stop
startup with a list of all errors found.

### Offline replay

Record a capture and replay it without the radio:

```sh
nc 192.168.1.50 30002 > testdata/my-capture.txt
```

```yaml
source:
  url: file://testdata/my-capture.txt
  replaySpeed: 0      # 0 = as fast as possible, 1.0 = real time (uses @-timestamps)
```

## HTTP API

| Endpoint | Description |
|---|---|
| `GET /api/aircraft` | Current aircraft. `?withPosition=true`, `?sort=distance\|altitude\|lastSeen` |
| `GET /api/aircraft/{icao}` | One aircraft, or 404 |
| `GET /api/stats` | Source state, frame counts per DF, CRC failures, drops, CPR rejections, messages/s |
| `GET /api/stream` | Server-Sent Events: the aircraft array every `api.streamInterval` |
| `GET /api/ws` | WebSocket used by the UI: `{"type","data"}` messages, `receiver` and `map` once, then `aircraft` and `stats` every `api.streamInterval` |
| `GET /api/map` | Tile layer for the UI from the `map` config section |
| `GET /api/receiver` | Receiver position and `maxRangeNm` from the config |
| `GET /healthz` | 200 if a frame arrived within the last 30 s, otherwise 503 |

Every other path serves the [web UI](#web-ui) embedded in the binary. Fields that are unknown are left out of the JSON, so `"altBaro": 0` means a real altitude of 0 ft.

Sample output from a replay of the synthetic `testdata/capture.txt`:

```sh
$ curl -s 'localhost:8080/api/aircraft?withPosition=true'
```

```json
[
  {
    "icao": "40621D",
    "country": "United Kingdom",
    "altBaro": 38000,
    "onGround": false,
    "lat": 52.2572021484375,
    "lon": 3.91937255859375,
    "posTime": "2026-11-19T10:40:31.061477+02:00",
    "distanceNm": 15.73,
    "bearing": 349.1,
    "firstSeen": "2026-11-19T10:40:28.061477+02:00",
    "lastSeen": "2026-11-19T10:40:31.061477+02:00",
    "messages": 2,
    "messagesByDf": { "17": 2 },
    "hasPosition": true,
    "adsb": true
  }
]
```

```sh
$ curl -s localhost:8080/api/stats
```

```json
{
  "uptimeSeconds": 0,
  "source": {
    "url": "file://testdata/capture.txt",
    "connected": false,
    "reconnects": 0,
    "malformedLines": 2,
    "lastFrame": "2026-11-19T10:40:28.061482+02:00"
  },
  "frames": 16,
  "framesPerDf": { "11": 1, "17": 10, "4": 3, "5": 2 },
  "messages": 12,
  "messagesPerSecond": 0.2,
  "crcFailures": 2,
  "fixedBitErrors": 0,
  "unsupported": 0,
  "invalid": 1,
  "addressParityDrops": 1,
  "positions": 2,
  "cprRejected": 0,
  "aircraft": 5
}
```

## Web UI

Open `http://<host>:8080/` for a live map and a table of aircraft. The UI is built into the binary, so
there's nothing else to deploy.

- **Map** (Leaflet, OpenStreetMap tiles, no API key): the receiver with range rings at 25/50/100 nm, and
  aircraft rotated by track and colored by altitude (legend bottom-left). Labels show callsign and altitude
  in hundreds of feet. Trails cover the last 5 minutes. Aircraft whose position is older than 30 s fade out.
  The browser loads the tiles itself, from openstreetmap.org by default, so the map needs internet access
  unless you use your own tile server (see below).
- **Table:** every aircraft, including Mode-S-only ones (marked "no pos"). Every column sorts, with unknown
  values last. Emergencies (7500/7600/7700 or an ADS-B emergency state) are red and pinned to the top.
  Filter by callsign, ICAO or squawk, or show only aircraft with a position.
- **New aircraft:** rows of aircraft first seen in the last 30 s are highlighted in amber, fading out, and
  their map marker pulses briefly when it appears.
- **Selection:** clicking a marker or a row highlights the aircraft in both, opens a detail panel with every
  known field, and links to ADS-B Exchange. Clicking a row pans the map to the aircraft.
- **Live data:** the page receives everything over one WebSocket (`/api/ws`) and never polls. The header
  shows the aircraft count, messages per second, the receiver source status and the connection state. The
  page reconnects with backoff by itself.
- Light and dark themes follow the OS setting. On narrow screens the map sits above the table; on wide
  screens a splitter between them can be dragged.

### Own tile server

Set the `map` section of the config to any XYZ tile server, for example a self-hosted one on your LAN. The map
then works without internet access:

```yaml
map:
  tileUrl: http://tiles.lan:8080/styles/basic/{z}/{x}/{y}.png   # must contain {z}, {x}, {y}; {s} optional
  attribution: '&copy; OpenStreetMap contributors'
  maxZoom: 18
  darkFilter: false   # true inverts the tiles in dark mode; turn off if your style is already dark
```

The URL must be reachable from the browser, not just from the server. Changing it needs only a restart, not a
rebuild.

Tips for a self-hosted server:

- **The URL format depends on the server.** Typical forms are:

  | Server | `tileUrl` |
  |---|---|
  | [openstreetmap-tile-server](https://github.com/Overv/openstreetmap-tile-server) (Docker) | `http://<host>:8080/tile/{z}/{x}/{y}.png` |
  | [TileServer GL](https://github.com/maptiler/tileserver-gl) | `http://<host>:8080/styles/<style>/{z}/{x}/{y}.png` |

  Open a single tile such as `.../0/0/0.png` in the browser first to confirm the format.
- **No CORS setup is needed.** Leaflet loads tiles as plain images.
- **Don't mix HTTP and HTTPS.** If the UI is served over HTTPS, the tile server must use HTTPS too, or the
  browser blocks the tiles as mixed content.
- **Match `maxZoom` to what the server renders.** If it only has tiles up to zoom 16, set `maxZoom: 16`;
  otherwise zooming further shows blank tiles.
- **`darkFilter`:** keep it `true` for a standard light OSM style, and set it to `false` for a dark style.
- **When tiles don't appear,** check the tile requests in the browser's network tab for the URL and status
  code.

### Building

```sh
make build        # npm ci + vite build into web/dist/app, then go build: one self-contained binary
make run          # build and run with config/config.yaml
```

Without Node, `go build ./...` still works and serves a placeholder page instead of the UI.

### UI development

```sh
go run ./cmd/df17-decoder -config config/config.yaml    # backend on :8080
cd web && npm run dev                                   # UI with hot reload on http://localhost:5173
```

The Vite dev server proxies `/api` (including the WebSocket) to `http://localhost:8080`. Set
`DF17_BACKEND=http://host:port` to proxy elsewhere. Run `make test` and `make lint` for Go and UI tests,
ESLint and `tsc`.

### Demo without a radio

```sh
make demo         # generates testdata/busy.txt (150 aircraft over 10 min) and replays it in real time
make busy         # only regenerates testdata/busy.txt
```

`make demo` serves on port 8080 (`config/demo.yaml`). Stop any other instance first, or change `api.listen` there.

`cmd/replaygen` writes the synthetic capture around the receiver in `config/demo.yaml` (`-config`). By default
40% of the aircraft arrive during the replay, with airliners entering at the edge of coverage, so new rows keep
appearing. Another 30% stop transmitting early and then expire. About 15% are Mode-S-only, and one squawks 7700.
Flags such as `-n`, `-duration`, `-arrivals`, `-departures`, `-modes-only`, `-range`, `-lat` and `-lon`
change this (`go run ./cmd/replaygen -h`).

## Architecture

```
source (AVR / Beast / file) ──chan Frame──▶ app: modes.Decode ──▶ tracker.Update ──▶ api (JSON, WebSocket, SSE) ──▶ web UI
                                               │                      │
                                               └──── stats ◀──────────┘
```

- `internal/source`: TCP readers for AVR and Beast with exponential-backoff reconnect, and file replay paced by
  12 MHz timestamps.
- `internal/modes`: pure decoding with no I/O. It covers CRC-24, all DFs, ME type codes, Gillham, and CPR.
  Each decoder cites its section of [The 1090 MHz Riddle](https://mode-s.org/1090mhz/).
- `internal/tracker`: the aircraft table (one writer, many readers via copies), the known-address filter, CPR
  position resolving with range and speed checks, and TTL expiry.
- `internal/api`: HTTP handlers, the `/api/ws` WebSocket and the embedded UI. `internal/stats`: atomic
  counters. `internal/config`: YAML defaults and validation. `internal/geo`: distance and bearing.
- `web/`: React + TypeScript UI (Vite, Leaflet, TanStack Table), embedded through `web/embed.go`.
  `web/src/api/aircraft.fixture.json` is a contract shared by a Go test and a TypeScript test, so the JSON
  and the TS types can't drift apart.
- `cmd/replaygen`: synthetic AVR capture generator (its own frame encoders, checked against the decoder).

## Development

```sh
make test         # go test -race ./... and Vitest
make lint         # go vet, gofmt, ESLint, tsc
go fix ./...
```

`internal/modes` tests use the known vectors from mode-s.org. `internal/app` runs `testdata/capture.txt`
through the whole pipeline and checks exact counts.
