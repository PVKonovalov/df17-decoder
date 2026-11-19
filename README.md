# df17-decoder

A Mode S / ADS-B decoder written from scratch in Go. It reads raw 1090 MHz frames from
[dump1090](https://github.com/flightaware/dump1090) and decodes them itself. It does not use dump1090's decoded
outputs (SBS, JSON) or any third-party ADS-B library. Live aircraft state is served over an HTTP/JSON API and a
WebSocket, and shown in a built-in web UI with a map and a table.

What it decodes:

- **DF17/18 extended squitter:** callsign and emitter category, barometric and GNSS altitude, airborne position
  (global and local CPR), surface position with taxi speed and track (decoded against the receiver within 45 NM),
  target state (autopilot/FMS selected altitude and heading, altimeter setting, autopilot modes), velocity (ground speed or airspeed with heading, vertical rate), emergency state and
  squawk, ADS-B version.
- **Non-ADS-B Mode S:**
  - DF11 all-call replies.
  - DF0/4/16/20 altitude and DF5/21 squawk, accepted via address/parity only for addresses already seen in a
    valid DF11/17/18 frame.
  - Comm-B replies (DF20/21): BDS 2,0 callsign, and Enhanced Mode S BDS 4,0 (selected altitude, altimeter),
    5,0 (roll, track, ground speed, TAS) and 6,0 (heading, IAS, Mach, vertical rate). These registers aren't
    labelled in the reply, so each is inferred from its layout, value ranges and IAS/Mach consistency at the
    reply's altitude. A reply that fits several registers is used only when exactly one agrees with the
    aircraft's recent ADS-B or Comm-B data, and a report that contradicts that data is rejected.
    `commBAmbiguous` and `commBRejected` in `/api/stats` count both cases.
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
go run ./cmd/df17-decoder -version   # print the version and exit
```

Without `-config` the service runs on built-in defaults (`beast://localhost:30005`, API on `:8080`). Every key is
documented in [`config/config.example.yaml`](config/config.example.yaml). Unknown keys and invalid values stop
startup with a list of all errors found.

### Offline replay

Record a capture and replay it without the radio. Both formats work, and the format is detected from the file.
A Beast capture keeps the signal level (RSSI):

```sh
nc 192.168.1.50 30005 > testdata/my-capture.bin   # Beast binary, with signal levels
nc 192.168.1.50 30002 > testdata/my-capture.txt   # or AVR text
```

```yaml
source:
  url: file://testdata/my-capture.bin
  replaySpeed: 0      # 0 = as fast as possible, 1.0 = real time (uses the 12 MHz timestamps)
```

## Running as a service

### Binaries and version

```sh
make build   # ./df17-decoder for this machine
make dist    # static binaries in dist/ for linux/arm64 (64-bit Raspberry Pi OS), linux/arm (32-bit), linux/amd64
```

The version comes from the nearest git tag (`git describe --tags --always --dirty`, e.g. `v1.0.0` or
`v1.0.0-3-g4a2b4d4`). `make` links it into the binary. A plain `go build` or `go install …@v1.0.0` falls back to the
version Go records itself. `-version` prints it, the service logs it at startup, `/api/stats` reports it, and the
UI shows it when you hover over the title. To release, tag the commit (`git tag v1.1.0`) and run `make dist`.

### systemd

[`deploy/df17-decoder.service`](deploy/df17-decoder.service) runs the binary as an unprivileged, sandboxed user and
restarts it on failure (not on a config error, exit code 2). On the receiver, for example a Raspberry Pi running
dump1090-fa:

```sh
sudo install -m 755 dist/df17-decoder-v1.1.2-linux-amd64 /usr/local/bin/df17-decoder
sudo install -D -m 644 config/config.yaml /etc/df17-decoder/config.yaml
sudo install -m 644 deploy/df17-decoder.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now df17-decoder
journalctl -u df17-decoder -f   # logs
```

After editing `/etc/df17-decoder/config.yaml`, run `sudo systemctl restart df17-decoder`. The unit gives the
service one writable directory, `/var/lib/df17-decoder`, and makes it the working directory, so
`coverage.file: coverage.json` (or the full `/var/lib/df17-decoder/coverage.json`) keeps the coverage outline and
grid across restarts. A `coverage.file` whose directory is missing or not writable is a config error: the service
stops with exit code 2 and systemd doesn't restart it. To listen on a port
below 1024, uncomment the `CAP_NET_BIND_SERVICE` lines in the unit.

### Docker

```sh
make docker   # builds df17-decoder:<version> and :latest (UI, static binary, distroless non-root image)
docker run -d --name df17 --restart unless-stopped -p 8080:8080 \
  -v "$PWD/config/config.yaml:/etc/df17-decoder/config.yaml:ro" \
  -v df17-state:/var/lib/df17-decoder df17-decoder
```

The `df17-state` volume keeps the coverage data when `coverage.file` is `/var/lib/df17-decoder/coverage.json`.
Use the full path here: the image's working directory is `/`, which the non-root user can't write.

In the container, `localhost` is the container itself. Point `source.url` at the dump1090 host's address, or use
`--network host` on Linux. For another architecture, use buildx, for example
`docker buildx build --platform linux/arm64 --build-arg VERSION=$(git describe --tags) -t df17-decoder .`.

## HTTP API

| Endpoint | Description |
|---|---|
| `GET /api/aircraft` | Current aircraft. `?withPosition=true`, `?sort=distance\|altitude\|lastSeen` |
| `GET /api/aircraft/{icao}` | One aircraft, or 404 |
| `GET /api/stats` | Version, source state, frame counts per DF, CRC failures, drops, CPR rejections, messages/s |
| `GET /api/stream` | Server-Sent Events: the aircraft array every `api.streamInterval` |
| `GET /api/ws` | WebSocket used by the UI: `{"type","data"}` messages, `receiver` and `map` once, `coverage` every 10 s, `coverageGrid` every 60 s, `aircraft` and `stats` every `api.streamInterval` |
| `GET /api/map` | Tile layer for the UI from the `map` config section |
| `GET /api/receiver` | Receiver position and `maxRangeNm` from the config |
| `GET /api/coverage` | Range outline: the furthest accepted position per 2° of bearing (distance, point, altitude, time) |
| `GET /api/coverage/grid` | Coverage grid of 2° × 5 NM cells per altitude band (<10k, 10–25k, ≥25k ft, unknown): positions, and average/min/max signal in dBFS |
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
  "version": "v1.0.0",
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
  "commBAmbiguous": 0,
  "commBRejected": 0,
  "aircraft": 5
}
```

## Web UI

Open `http://<host>:8080/` for a live map and a table of aircraft. The UI is built into the binary, so
there's nothing else to deploy.

- **Map** (Leaflet, OpenStreetMap tiles, no API key): the receiver with range rings at 25/50/100 nm, and
  aircraft rotated by track and colored by altitude (legend bottom-left). With Beast input, the legend has an
  Altitude/Signal switch that colors markers by signal level instead, from red (weak, -36 dBFS) to green
  (strong, -3 dBFS), to check antenna coverage. RSSI is averaged over each aircraft's last 8 frames.
  The **Coverage** switch in the legend has three views. **Outline** draws the furthest position seen in each
  2° of bearing, so blocked directions show up as dents; hover it for the range, bearing and altitude.
  **Signal** (Beast input) and **Count** are heatmaps of 2° × 5 NM wedges around the receiver, colored by the
  average signal level or by how many positions were decoded there (log scale), for all altitudes or one band;
  hover a wedge for its numbers. The signal average only covers decoded frames, so cells near the edge look
  stronger than they are; use Count to see where reception thins out. Set `coverage.file` to
  keep the outline and the grid across restarts. It is saved every minute and on shutdown, and starts over when the
  receiver position changes. Labels show callsign and altitude
  in hundreds of feet, or "GND" for aircraft on the ground, which are drawn in a neutral color. Trails cover
  the last 5 minutes. Aircraft whose position is older than 30 s fade out.
  The browser loads the tiles itself, from openstreetmap.org by default, so the map needs internet access
  unless you use your own tile server (see below).
- **Table:** every aircraft, including Mode-S-only ones (marked "no pos"). Every column sorts, with unknown
  values last. Emergencies (7500/7600/7700 or an ADS-B emergency state) are red and pinned to the top.
  Filter by callsign, ICAO or squawk, or show only aircraft with a position.
- **New aircraft:** rows of aircraft first seen in the last 30 s are highlighted in amber, fading out, and
  their map marker pulses briefly when it appears.
- **Selection:** clicking a marker or a row highlights the aircraft in both, opens a detail panel with every
  known field (including the selected altitude and heading, altimeter setting, autopilot modes, roll and
  Mach), and links to ADS-B Exchange. Clicking a row pans the map to the aircraft.
- **Shareable links:** the address bar follows the selection and the table's filter and sort, e.g.
  `/?icao=738ABC&q=ELY&pos=1&sort=-altitude` (`-` sorts descending; defaults are left out). Opening such a
  link selects the aircraft and pans to it as soon as it is heard. **Copy link** in the detail panel copies
  the address, also over plain http on a LAN address.
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
make demo         # generates testdata/busy.bin (150 aircraft over 10 min) and replays it in real time
make busy         # only regenerates testdata/busy.bin
```

`make demo` serves on port 8080 (`config/demo.yaml`). Stop any other instance first, or change `api.listen` there.

`cmd/replaygen` writes the synthetic capture around the receiver in `config/demo.yaml` (`-config`). `make busy`
writes Beast (`-format beast`), with a signal level that falls with distance, so the Signal view works. The
default is AVR text. By default 40% of the aircraft arrive during the replay, with airliners entering at the edge
of coverage, so new rows keep appearing. Another 30% stop transmitting early and then expire. About 15% are
Mode-S-only (half of them also answer Comm-B with BDS 4,0/5,0/6,0), one squawks 7700, and 6 more taxi at an
airfield 6 nm north-east of the receiver (surface positions, "GND" on the map). Flags such as `-n`, `-taxiing`,
`-duration`, `-arrivals`, `-departures`, `-modes-only`, `-range`, `-lat` and `-lon` change this
(`go run ./cmd/replaygen -h`).

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
