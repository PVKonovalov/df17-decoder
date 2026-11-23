# df17-decoder

A Mode S / ADS-B decoder written from scratch in Go. It reads raw 1090 MHz frames from
[dump1090](https://github.com/flightaware/dump1090) and decodes them itself. It does not use dump1090's decoded
outputs (SBS, JSON) or any third-party ADS-B library. Live aircraft state is served over an HTTP/JSON API and a
WebSocket, and shown in a built-in web UI with a map and a table.

What it decodes:

- **DF17/18 extended squitter:** callsign and emitter category, barometric and GNSS altitude, airborne position
  (global and local CPR), surface position with taxi speed and track (decoded against the receiver within 45 NM),
  target state (autopilot/FMS selected altitude and heading, altimeter setting, autopilot modes), velocity (ground speed or airspeed with heading, vertical rate), emergency state and
  squawk, ADS-B version, position accuracy (NACp, NIC supplement-A, SIL from TC 31; NIC from the position type
  code) and TC 0 "no position" messages, from which each ADS-B aircraft gets a position quality: `ok`, `degraded`
  (NIC < 7 or NACp < 8) or `lost` (TC 0, or no position for 30 s while other messages arrive), e.g. under GNSS jamming.
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
- **Trails and history:** the server keeps the last `trails.window` (10 min) of every aircraft's track, so a newly
  opened window shows existing trails at once. Optionally, every flight and its thinned track is written to an
  SQLite database (see [Flight history](#flight-history)).

The backend uses the Go standard library plus `gopkg.in/yaml.v3`, `github.com/gorilla/websocket` and
`modernc.org/sqlite` (pure Go, no cgo, so the binaries stay static), and requires Go 1.26. Building the web UI also needs Node 26+ and npm. Without Node the binary still builds and serves a
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
sudo install -m 755 dist/df17-decoder-v1.2.0-linux-amd64 /usr/local/bin/df17-decoder
sudo install -D -m 644 config/config.yaml /etc/df17-decoder/config.yaml
sudo install -m 644 deploy/df17-decoder.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now df17-decoder
journalctl -u df17-decoder -f   # logs
```

After editing `/etc/df17-decoder/config.yaml`, run `sudo systemctl restart df17-decoder`. On SIGTERM the service
finishes cleanly within a second: frames already received are still recorded and decoded, the tape segment is
closed, the coverage file saved, the history written out and its database closed. A restart doesn't split flights or
lose the hour's statistics (see below). The unit gives the
service one writable directory, `/var/lib/df17-decoder`, and makes it the working directory, so
`coverage.file: coverage.json` (or the full `/var/lib/df17-decoder/coverage.json`) keeps the coverage outline and
grid across restarts, and the default `history.file: history.db` and `tape.dir: tape` land there too. A
`coverage.file`, `tape.dir` or (with history enabled) `history.file` whose directory is missing or not writable
is a config error: the service stops with exit code 2 and systemd doesn't restart it. To listen on a port
below 1024, uncomment the `CAP_NET_BIND_SERVICE` lines in the unit.

### Docker

```sh
make docker   # builds df17-decoder:<version> and :latest (UI, static binary, distroless non-root image)
docker run -d --name df17 --restart unless-stopped -p 8080:8080 \
  -v "$PWD/config/config.yaml:/etc/df17-decoder/config.yaml:ro" \
  -v df17-state:/var/lib/df17-decoder df17-decoder
```

The image's working directory is the state directory `/var/lib/df17-decoder`, as under systemd, so relative paths
(the default `tape.dir: tape`, `history.file: history.db`, `coverage.file: coverage.json`) land on the
`df17-state` volume. Size the volume for the tape (about 1 GB for the default 14 days, see
[Tape recorder](#tape-recorder-and-playback)). Keep the database on a volume, not a bind mount on a network file
system: SQLite's WAL mode needs local file locking.

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
| `GET /api/ws` | WebSocket used by the UI: `{"type","data"}` messages, `receiver`, `map` and `trails` (every current trail and the window) once, `coverage` every 10 s, `coverageGrid` every 60 s, `trailPoints` (only points this connection hasn't had), `aircraft`, `stats` and (with playback) `playback` every `api.streamInterval`. The client may send playback commands, see [Playback](#playback) |
| `GET /api/map` | Tile layer for the UI from the `map` config section |
| `GET /api/receiver` | Receiver position and `maxRangeNm` from the config |
| `GET /api/coverage` | Range outline: the furthest accepted position per 2° of bearing (distance, point, altitude, time) |
| `GET /api/coverage/grid` | Coverage grid of 2° × 5 NM cells per altitude band (<10k, 10–25k, ≥25k ft, unknown): positions, and average/min/max signal in dBFS |
| `GET /api/history/flights` | Stored flights overlapping `?from=&to=` (RFC 3339 or unix ms), newest first; `?icao=`, `?q=` (ICAO or callsign prefix), `?limit=` (100, max 1000) |
| `GET /api/history/flights/{id}` | One stored flight with all its track points and altitude/squawk samples, or 404 |
| `GET /api/history/at?t=` | Every flight's last position and sample at or before `t`, at most `tracker.ttl` old: the state of the sky at `t` |
| `GET /api/history/timeline?from=&to=&bucket=1m` | Flights active per bucket as `[ADS-B, Mode-S only]` (at most 2000 buckets) and emergency squawk events: the timeline's density strip |
| `GET\|PUT\|DELETE /api/reference/aircraft/{icao}` | Your own data for an airframe: `registration`, `typeCode`, `typeName`, `operator` (each up to 64 characters). GET returns 404 when there is none, PUT returns the entry (204 and deletes it when every field is empty). 503 without history |
| `GET\|PUT\|DELETE /api/reference/routes/{callsign}` | Your own route for a callsign (1–8 letters or digits): `origin`, `destination`, the same way |
| `GET /api/storage` | Disk use of the state directory: volume size and free space, tape/database/other bytes, growth per day, steady-state sizes, days until full and until the disk guard, the oldest tape/flight/rollup, guard flags. Cached for 60 s |
| `GET /api/stats/hourly?from=&to=` | Hourly rollups (aircraft ADS-B / Mode-S only, ADS-B aircraft by position quality, flights started, positions, messages, max range and bearing, sizes, receiver counters), plus the current hour so far (`partial`). Default the last 24 h; at most 400 days, clamped to `stats.retention` |
| `GET /api/stats/countries?from=&to=&limit=` | Aircraft-hours per country of registration, largest first (`limit` 20, max 500) |
| `GET /api/stats/operators?from=&to=&limit=` | Aircraft-hours per callsign prefix (airline-style callsigns only) |
| `GET /api/stats/range-daily?from=&to=&tz=` | The furthest position per day in the IANA timezone `tz` (default UTC), with its bearing and hour |
| `GET /api/stats/altitude?from=&to=` | Stored positions per altitude band: ground, 5000 ft bands up to ≥40k, unknown |
| `GET /api/stats/notable?from=&to=&limit=` | Emergency squawks, position losses (`gnssLost`) and first sightings of ICAO addresses: counts and up to `limit` (100) events, emergencies first, then losses, then newest |
| `GET /api/tape/extent` | The tape's oldest and newest recorded time and its recorded spans (silences over 60 s split them); 503 when the tape is disabled |
| `GET /healthz` | 200 if a frame arrived within the last 30 s, or the source is still connected after delivering frames (`idle`: a quiet sky sends nothing); 503 before the first frame, or when the source is disconnected and silent. A decoder that stays connected but stops sending looks idle, so check `source.lastFrame` in `/api/stats` for that |

The `/api/history` endpoints return 503 when history is disabled. Every other path serves the [web UI](#web-ui)
embedded in the binary. Fields that are unknown are left out of the JSON, so `"altBaro": 0` means a real altitude of 0 ft.

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

## Flight history

Track points are thinned before they are kept: a new point is stored for the first fix in each `trails.minInterval`
(5 s) slot of the clock, when the track has turned by 3° or more, the altitude has changed by 200 ft or more, or the aircraft has landed or
taken off. The live trails and the database use the same points.

A flight is one continuous presence of an ICAO address. It opens with the first message, stays open while the
aircraft is tracked and for `history.flightGap` (10 min) after it expires, so an aircraft that briefly drops out
continues the same flight. It closes after the gap, or when the callsign changes to another non-empty value
(which starts a new flight). Mode-S-only aircraft get flights too, with samples of altitude and squawk (on a
change of 100 ft or a new squawk, at least every 30 s) instead of positions.

```yaml
history:
  enabled: true
  file: history.db      # Docker: /var/lib/df17-decoder/history.db
  flushInterval: 15s    # one transaction per interval; a crash loses at most this much
  flightGap: 10m
  retention: 720h       # closed flights older than this are deleted hourly; 0 keeps everything
```

Writing never slows decoding: points go through a bounded queue to one writer goroutine, and are dropped (and
counted) if the database can't keep up. `/api/stats` then reports `historyFlightsOpen`, `historyPointsWritten`,
`historyDropped`, `historyWriteErrors` and `historyLastFlush`. With a live source (`tcp://`, `beast://`) a
shutdown leaves the open flights open, and the next start continues each one whose aircraft was heard within
`tracker.ttl` + `history.flightGap` (with its trail on the map), so a restart doesn't split flights. Older open
flights, and all of them with a file or tape source, are closed at start; a crash is handled the same way. The schema version is kept in the database and upgraded automatically.

**Reference data.** Mode S doesn't carry the aircraft type or the route, so you enter them yourself, with
**Edit** in the detail panel. They are kept in the same database, in `aircraft_info` (by ICAO address:
registration, type code and name, operator) and `routes` (by callsign: from and to), which share their keys
with `flights.icao` and `flights.callsign`. Retention never deletes them, and no outside service is used.

**Disk use.** A stored point takes about 84 bytes including indexes. An aircraft in cruise stores about one point
per 5 s (about 1.5 MB per day), more while turning or climbing, so count roughly 2 MB per day for every aircraft
in view on average: 10 aircraft around the clock take about 600 MB per 30 days, 100 about 6 GB. Samples of
Mode-S-only aircraft are much smaller. Deleted flights free their space through incremental vacuum.

**Privacy.** Like `coverage.json`, the database reveals where your receiver is: every track ends at the edge of
its range. Don't share the file, and keep real coordinates out of anything you publish. `*.db`, `*.db-wal`
and `*.db-shm` are ignored by git and Docker builds.

## Statistics

The Statistics page (`/stats`, linked from the header) shows where the disk goes and what the receiver heard over
24 hours, 7 days, 30 days, a year or a custom span of days. The range is kept in the URL, so a view can be shared.
It refreshes once a minute and doesn't use the live WebSocket.

- **Storage:** a bar of tape, flight history, other files and free space, and in plain words how fast the tape and
  the database grow and where they level off, e.g. "The tape grows about 36 MB per day; it levels off at about
  500 MB after 14 days". Growth is measured from the hourly size samples (falling back to the last day of tape
  segments); shrinking steps are retention at work and don't count. Steady state is growth × retention. Data kept
  forever (`retention: 0`) counts towards "the disk is full in…", everything still growing towards "the disk
  guard starts deleting in…". An amber warning appears below twice `tape.minFreeMB` free or after the guard has
  deleted segments, a red one when it is deleting or recording is paused.
- **Aircraft per hour:** distinct ICAO addresses heard in each hour, stacked as ADS-B (any ADS-B flight in the
  hour) and Mode-S only, with decoded messages on a second axis. The current hour is lighter.
- **Hour × weekday:** the average aircraft per hour for each weekday and hour, in the browser's timezone.
- **Countries:** aircraft-hours by country of registration (from the ICAO address): each aircraft counts once
  per hour it was heard, so long ranges add up without keeping every flight. The top 15, the rest as "Other".
- **Operators:** aircraft-hours by airline designator, the first 3 letters of callsigns like `KLM1023`. Other
  callsigns (registrations, most military and GA) aren't counted.
- **Altitude:** stored positions per 5000 ft band, plus on the ground and unknown. Track points are kept about
  every 5 s (more while turning or climbing), so this is close to the time spent at each altitude.
- **Max range per day:** the furthest stored position each day, with the best day marked; useful for comparing
  antennas.
- **ADS-B position quality:** ADS-B aircraft per hour by the worst position quality they reported in it: lost,
  degraded, no position received at all (no loss reported, usually range or too few frames), or OK, with the share
  lost or degraded as a line. A weekday × hour heatmap of that share shows recurring GNSS interference. Hours rolled
  up before this was recorded are recomputed while their flights are stored, but without losses (none were kept).
- **Receiver health:** frames per hour, the share failing CRC, and silent minutes (minutes without a single frame,
  whether the receiver died or the connection dropped) in red. The tooltip adds bit errors fixed, CPR rejections,
  reconnects and the mean signal level (Beast only). Like messages, these exist only for hours the service ran
  through from start to end.
- **Notable aircraft:** emergency squawks (7500, 7600, 7700; when first seen in the flight), flights that lost their
  position (first time in the flight) and ICAO addresses heard for the first time. Each row opens playback at that moment with the aircraft selected. "First" means since the
  flight history began: the first sightings of addresses are remembered for `stats.retention`, longer than flights.
  When upgrading an existing database, every aircraft already in it is "new" at its first stored flight, so the
  list becomes meaningful after a while.

Clicking an hour bar, a heatmap cell (its latest hour) or a day opens the map in playback at that hour.

The charts come from hourly rollups in the flight history database, so they need `history.enabled`. A job
computes the previous hour at five past each hour; at start it backfills missing hours from the stored flights in
the background, and recomputes hours of an older rollup version while their flights are still stored. Messages
and the receiver counters are known for hours seen from their start: on shutdown the service saves the counters
of the unfinished hours (and of the previous hour while its rollup is pending), and the next run adds to them,
counting the downtime as silent minutes. They stay unknown (NULL) for the hour the service first started in, after
a crash, and for backfilled hours. Rollups are small (one row per hour plus one per country,
callsign prefix and altitude band heard: about 1–2 KB per hour at a busy site, 10–20 MB per year) and kept much longer than flights:

```yaml
stats:
  retention: 8760h   # rollups older than this are deleted daily; 0 keeps them forever
```

## Tape recorder and playback

The service records every frame it receives, 24/7, to compressed hourly files, and the UI can play any moment
back exactly as it looked live: map, table, trails, detail panel, signal levels, Comm-B data. Playback decodes the
raw frames again through a second, separate tracker per browser tab, so it never touches the live view, the
flight history, the coverage or the live stats.

```yaml
tape:
  enabled: true
  dir: tape             # relative to the working directory (the state dir under systemd and in Docker)
  segment: 1h           # must divide a day evenly
  flushInterval: 5s     # a crash loses at most this much
  retention: 336h       # 14 days; segments are deleted hourly; 0 keeps everything
  minFreeMB: 1024       # below this free space the oldest segments go, then recording pauses

playback:
  maxSessions: 4        # browser tabs in playback at once; 0 disables playback
  idleTimeout: 30m      # a session paused this long frees its memory; play rebuilds it
```

**Format.** `tape/YYYY/MM/DD/YYYYMMDDTHHMMSSZ.tape.gz`, one file per UTC segment. Inside the gzip stream: a header
(`DF17TAPE`, format version 1, segment start in unix ns), then per frame the time since the previous frame
(uvarint ns), the receiver's 48-bit 12 MHz timestamp, the Beast signal byte (0 for AVR), the length and the frame
bytes. The file being written is `….tape.gz.part`; it is renamed when the hour ends and finalized at the next start
after a crash. A cut-off tail reads fine up to the last flush.

**Disk use.** About 14 bytes per frame: at 55 frames/s, about 65 MB a day, 0.9 GB for the default 14 days.
`/api/stats` reports `tapeRecording`, `tapePaused`, `tapeOldest`, `tapeNewest`, `tapeBytes`, `tapeFramesWritten`,
`tapeDropped` and `tapeWriteErrors`. Recording never slows decoding: a full write queue drops (and counts) frames.

**Privacy.** A tape reveals where the receiver is, just like `coverage.json` and the history database. `/tape/`
and `*.tape.gz*` are ignored by git and Docker builds; don't publish tape files.

### Playback

The **tape deck** under the header:

- **● REC** is red while recording, also in a quiet sky (the tooltip then says how long nothing was heard); amber
  when frames are dropped, writes fail, the writer stops flushing, the source is disconnected or recording is
  paused because the disk is low (hover for the reason); grey when the tape is disabled. Ages are measured on the
  server's clock (`serverTime` in `/api/stats`), so a browser with a wrong clock doesn't raise false alarms.
- **⏮** goes to the oldest recording, **⏪** back 10 s (Shift: 1 min). In playback also **⏸** pause (**▶** play while paused),
  **⏩** speed (1×, 2×, 4×, 8×, 16×, 60×) and **⏭ LIVE**.
- The **tape counter** shows the playback time, or **LIVE** and the server's time, in local time with UTC beneath
  (each with its date when it isn't today's); click it to type a local date and time. It flashes when playback jumps over a silence of more than a minute.
- The **timeline** shows the recorded spans with gaps hatched; click or drag to seek, and zoom to 1 hour, 6 hours,
  1 day or the whole tape. With `history.enabled` it also shows the traffic (flights per minute, ADS-B darker) and
  red markers for emergency squawks, and the deck gets a **Find a flight** box: pick a callsign or ICAO to wind
  the tape to 30 s before the flight and select it.
- **Keyboard** (not while typing): Space play/pause, ←/→ ±10 s, Shift+←/→ ±1 min, L live.
- **Moment links:** `/?t=2026-11-20T10:00:00Z&speed=4&icao=738ABC` opens playback at that moment with that
  aircraft selected. **Copy link** in the detail panel includes the moment while in playback.

A seek rebuilds the state from the tape, starting `max(trails.window, tracker.ttl) + 1m` before the target, so
aircraft and trails look exactly as they did live. Pausing stops the clock, so nothing expires. Playback that
catches up with now returns to live by itself; at 1× it stays behind, like a time-shifted recording.

**Protocol.** On `/api/ws` the client sends `{"type":"playback","data":{"action":…}}` with `play`, `pause`,
`live`, `seek` (`"t"`: RFC 3339), `speed` (`"speed"`) or `step` (`"seconds"`, ±). While in playback, `aircraft`,
`trails` (a full one after every seek) and `trailPoints` come from the session; `stats` and coverage stay live.
Every update carries `{"type":"playback","data":{"mode","t","speed","playing","gapSkipped","tapeStart","tapeEnd"}}`.
An invalid command gets `{"type":"error","data":{"message"}}` back.

**Replaying a tape as the input.** `source.url: tape://<dir>?from=<RFC 3339>&speed=<x>` (speed 0: as fast as
possible) feeds a tape through the whole service instead of a receiver, for example on another machine; recording
is off while the input is a tape.

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
  in hundreds of feet, or "GND" for aircraft on the ground, which are drawn in a neutral color. Trails come
  from the server and cover the last `trails.window` (10 minutes), also in a window opened later. Aircraft whose position is older than 30 s fade out.
  The **icon** follows the ADS-B emitter category: heavy (A5, drawn larger), large (A3/A4), small (A2), light
  and ultralight (A1/B4), high performance (A6), helicopter (A7), glider (B1), balloon (B2, not turned), UAV (B6)
  and ground vehicles (C1/C2); Mode-S-only aircraft and other categories keep the generic icon. The legend lists
  the icons currently on the map, and the detail panel names the category, e.g. "A3 (large)".
  The browser loads the tiles itself, from openstreetmap.org by default, so the map needs internet access
  unless you use your own tile server (see below).
- **Table:** every aircraft, including Mode-S-only ones (marked "no pos"), with its map icon in the same color
  (altitude or signal, as chosen in the legend) and turned to its track. Every column sorts, with unknown
  values last. Emergencies (7500/7600/7700 or an ADS-B emergency state) are red and pinned to the top.
  Filter by callsign, ICAO or squawk, or show only aircraft with a position.
- **New aircraft:** rows of aircraft first seen in the last 30 s are highlighted in amber, fading out, and
  their map marker pulses briefly when it appears.
- **Selection:** clicking a marker or a row highlights the aircraft in both, opens a detail panel with every
  known field (including the selected altitude and heading, altimeter setting, autopilot modes, roll and
  Mach), and links to ADS-B Exchange. Clicking a row pans the map to the aircraft. With history enabled, the
  panel also shows the route, registration, aircraft type and operator you entered, and **Edit** changes them
  (see [Reference data](#flight-history)).
- **Panels:** the **Table** and **Legend** switches in the header hide the aircraft table (the map takes the
  full width) and the map legend with its color and coverage selectors. Each browser remembers the choice.
- **Statistics:** the header link opens the [Statistics](#statistics) page.
- **Shareable links:** the address bar follows the selection and the table's filter and sort, e.g.
  `/?icao=738ABC&q=ELY&pos=1&sort=-altitude` (`-` sorts descending; defaults are left out). Opening such a
  link selects the aircraft and pans to it as soon as it is heard. **Copy link** in the detail panel copies
  the address, also over plain http on a LAN address.
- **Live data:** the page receives everything over one WebSocket (`/api/ws`) and never polls. The header
  shows the aircraft count, messages per second, the receiver source status and the connection state. The
  page reconnects with backoff by itself.
- **Tape deck:** the bar under the header shows **● REC** and the playback controls, see
  [Playback](#playback).
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
airfield 6 nm north-east of the receiver (surface positions, "GND" on the map), one in three of them a service
vehicle (C2). ADS-B aircraft get a mix of emitter categories (mostly large, then heavy, small, light, some
helicopters, fast jets, a glider, a balloon and a UAV), so every map icon shows up; the light ones fly low and slow
under registration callsigns. Flags such as `-n`, `-taxiing`,
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
  position resolving with range and speed checks, TTL expiry, thinned trails and flight segmentation.
- `internal/tape`: the raw frame recorder (hourly gzip segments, retention, disk guard), the reader (seek across
  segments, gaps, extent) and the `tape://` source. `internal/playback`: per-connection sessions that decode the
  tape into their own tracker, aged by a virtual clock.
- `internal/history`: the optional SQLite store (a non-blocking `tracker.FlightSink`, batched writer, migrations,
  retention), its read queries, and the hourly statistics rollups. `internal/storage`: the disk report behind
  `/api/storage`.
- `internal/api`: HTTP handlers, the `/api/ws` WebSocket and the embedded UI. `internal/stats`: atomic
  counters. `internal/config`: YAML defaults and validation. `internal/geo`: distance and bearing.
- `web/`: React + TypeScript UI (Vite, Leaflet, TanStack Table, and ECharts on the lazily loaded Statistics
  page), embedded through `web/embed.go`.
  `web/src/api/aircraft.fixture.json` (and the trackpoint and stats fixtures) are contracts shared by a Go test and a TypeScript test, so the JSON
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
