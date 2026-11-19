# df17-decoder

A Mode S / ADS-B decoder written from scratch in Go. It reads raw 1090 MHz frames from
[dump1090](https://github.com/flightaware/dump1090) and decodes them itself. It does not use dump1090's decoded
outputs (SBS, JSON) or any third-party ADS-B library. Live aircraft state is served over an HTTP/JSON API.

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

It uses only the standard library plus `gopkg.in/yaml.v3`. Requires Go 1.26.

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
| `GET /api/ws` | WebSocket used by the UI: `{"type","data"}` messages, `receiver` once, then `aircraft` and `stats` every `api.streamInterval` |
| `GET /api/receiver` | Receiver position and `maxRangeNm` from the config |
| `GET /healthz` | 200 if a frame arrived within the last 30 s, otherwise 503 |

Every other path serves the web UI embedded in the binary (`make build` builds it first; without Node a
placeholder page is served). Fields that are unknown are left out of the JSON, so `"altBaro": 0` means a real altitude of 0 ft.

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

## Architecture

```
source (AVR / Beast / file) ──chan Frame──▶ app: modes.Decode ──▶ tracker.Update ──▶ api (snapshots, SSE)
                                               │                      │
                                               └──── stats ◀──────────┘
```

- `internal/source`: TCP readers for AVR and Beast with exponential-backoff reconnect, and file replay paced by
  12 MHz timestamps.
- `internal/modes`: pure decoding with no I/O. It covers CRC-24, all DFs, ME type codes, Gillham, and CPR.
  Each decoder cites its section of [The 1090 MHz Riddle](https://mode-s.org/1090mhz/).
- `internal/tracker`: the aircraft table (one writer, many readers via copies), the known-address filter, CPR
  position resolving with range and speed checks, and TTL expiry.
- `internal/api`: HTTP handlers. `internal/stats`: atomic counters. `internal/config`: YAML defaults and
  validation. `internal/geo`: distance and bearing.

## Development

```sh
go test -race ./...
go vet ./... && gofmt -l . && go fix ./...
```

`internal/modes` tests use the known vectors from mode-s.org. `internal/app` runs `testdata/capture.txt`
through the whole pipeline and checks exact counts.
