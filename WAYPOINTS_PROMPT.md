# Task: Waypoints from the config file on the map

## Context

Read `CLAUDE.md` first and follow its rules: ask before each milestone, add a line to `RELEASE.md` after each one, don't commit or push, run `go fix ./...`, keep comments sparse.

I want to list navigation waypoints (named fixes from the AIP) in the YAML config and see them on the map, both in live and playback modes. The coordinates use the AIP's compact degrees-minutes-seconds format, so I can paste them straight from the AIP. The format looks like this (fictional points):

```yaml
map:
  waypoints:
    - name: ALPHA
      lat: 250412.00N
      lon: 0225218.00E
      icon: Star
    - name: BRAVO
      lat: 2505.30
      lon: 22.95
      icon: Diamond
```

## Config (`internal/config`)

- **Location:** a new `map.waypoints` list, next to the existing tile settings. The default is an empty list.
- **Fields per waypoint:**

  | Field | Required | Meaning |
  | --- | --- | --- |
  | `name` | yes | 1–12 characters, shown as the label |
  | `lat` | yes | latitude: AIP DMS or decimal degrees (formats below) |
  | `lon` | yes | longitude: AIP DMS or decimal degrees (formats below) |
  | `icon` | no | one of the icon names below; case-insensitive, so `Star` works; default `triangle` |
  | `color` | no | `#rgb` or `#rrggbb`; default: a theme token that's readable on light and dark tiles |
  | `note` | no | free text shown in the tooltip |

- **Icons:** a fixed set of **10**, modelled on aeronautical chart symbols. Keep the list between 5 and 10; don't add more without asking me.

  | Name | Symbol | Typical use |
  | --- | --- | --- |
  | `triangle` | filled triangle | compulsory reporting point (the default) |
  | `triangleOpen` | outlined triangle | on-request reporting point |
  | `star` | four-pointed star | RNAV waypoint |
  | `diamond` | diamond | fix or intersection |
  | `circle` | filled circle | generic point |
  | `circleOpen` | outlined circle | airport or airfield |
  | `square` | outlined square | DME |
  | `hexagon` | outlined hexagon | VOR |
  | `cross` | ✕ | obstacle or hazard |
  | `flag` | small flag | visual reporting point |

  The names are matched case-insensitively (`TriangleOpen`, `star`). The API and UI use the lowercase form. Keep the set in **one place** on each side, a Go list and a TS list with the same names, and add a test that compares the two, so they can't drift.
- **Coordinate formats** for `lat`/`lon`, written in YAML as strings or numbers:
  - **AIP DMS:**
    - latitude `DDMMSS[.s…]N|S`, for example `250412.00N`
    - longitude `DDDMMSS[.s…]E|W`, for example `0225218.00E`
    - Exactly 2 or 3 degree digits as shown, minutes and seconds below 60, and the hemisphere letter required (case-insensitive). S and W are negative. The fractional seconds are optional (`250412N` works too).
  - **Decimal degrees:** a plain number, for example `25.0700` or `-0.5`.
  - Anything else is an error. In particular, a latitude written with `E`/`W` or a longitude with `N`/`S` is an error, not silently accepted.
- **New coordinate types:** put two types in `internal/geo`, so the axis is checked by the type, not by convention: `geo.Latitude` and `geo.Longitude`.
  - Each is a `float64` in decimal degrees underneath.
  - Each has its own parser (`ParseLatitude`, `ParseLongitude`) that accepts both formats above. A latitude rejects `E`/`W` and a 3-digit-degree DMS; a longitude rejects `N`/`S` and a 2-digit-degree DMS. Ranges: |lat| ≤ 90, |lon| ≤ 180.
  - Each implements `yaml.Unmarshaler` (YAML scalars written as strings or numbers), `encoding.TextUnmarshaler`, and `json.Marshaler` (a plain decimal number in the API).
  - Each has `DMS()`, which formats back to the AIP text (`250412.00N`, `0225218.00E`) for tooltips and logs, and `Degrees()`, which returns the `float64`.
  - The error messages say what's wrong, for example `minutes must be < 60` or `hemisphere N is not valid for a longitude`. The config layer adds the field path.
- **Use the new types for the receiver too:** change `receiver.lat` and `receiver.lon` to `geo.Latitude` and `geo.Longitude`, so the receiver position can also be written in AIP format. Existing configs with decimal numbers must load unchanged; add a test. Internal code that needs a `geo.Point` converts once, at config load. Don't spread the new types through the tracker and resolver.
- **Validation:** report all errors together, as `Load` already does. Each error names the waypoint's index and name, for example `map.waypoints[3] (BRAVO): lat "326015N": minutes must be < 60`. Reject duplicate names (case-insensitive) and unknown icons and keys (`KnownFields` is already on).
- **Optional:** `map.waypointsFile`, a path to a separate YAML file with the same list format. AIP lists can have hundreds of points, and this keeps the main config readable. Entries from the file and from the inline list are merged. A duplicate name across the two is an error. A relative path is relative to the config file's directory.
- **`config/config.example.yaml`** must still equal `Default()` (empty list). Document the formats in comments, and give a commented-out example with **fictional** points near the placeholder receiver in the example file. Never use real waypoints (see the privacy rule).

## API

- Add the waypoints to the existing map configuration that the UI already receives (`/api/map` and the `map` WebSocket message): `waypoints: [{name, lat, lon, icon, color?, note?}]` in decimal degrees, with the icon names lowercased.
- Update `api/types.ts`, the contract fixture and the contract tests, as `CLAUDE.md` requires.
- Config changes take effect after a restart. Hot reload is out of scope.

## Map (`web/src/map/`)

- **`WaypointLayer`:** one Leaflet layer, drawn **below** aircraft, trails and the coverage heatmap, so it never hides traffic.
- **Icons:** small SVG symbols (about 12 px) for the ten icon names. Write them in `icons.ts`, next to the aircraft icon helpers, with outline and fill colors from CSS tokens so they're readable in light and dark modes and with `darkFilter`. Use `color` when it's set.
- **Labels:** the name, in small text beside the symbol. Show labels only from zoom `map.waypointLabelMinZoom` (new config, default 9); below that, symbols only, so a zoomed-out map with many points stays clean.
- **Tooltip** on hover:
  - the name
  - the coordinates in the AIP format they were configured in, plus decimal degrees
  - the distance and bearing from the receiver, using the existing `geo` helpers on the client or values from the API
  - the note, if any
- **Not interactive otherwise:** clicking a waypoint doesn't select anything or pan, and the map's aircraft clicks still work through it.
- **Legend:** a "Waypoints" on/off switch next to the existing coverage controls, remembered per browser with `useStored` (`df17.show.waypoints`, on by default). Hide the switch when there are no waypoints.
- **Performance:** markers are created once (the list is static) and never re-created on snapshots. Check with 500 waypoints in `make demo` that panning stays smooth. Use a canvas renderer or plain SVG markers, whichever profiles better.
- The layer works the same in live and playback modes.

## Privacy rule

**Never put a real receiver position in code, tests, fixtures, docs or examples.** That includes **real waypoints near the receiver**: a local fix in a committed file reveals the area as clearly as coordinates do. My real waypoints go only into my gitignored `config/config.yaml`, or into a gitignored waypoints file; add a pattern for it (for example `config/waypoints*.yaml`) to `.gitignore` and `.dockerignore`.

Tests, fixtures, the README and the demo use fictional points around the placeholder receiver. For `make demo`, add a few fictional waypoints to `config/demo.yaml`, so they show in the demo and in screenshots.

## Tests

- **Coordinate types** (`geo.Latitude`, `geo.Longitude`): table-driven, with:
  - valid DMS with and without fractional seconds
  - S/W giving negative values
  - lowercase hemisphere letters
  - 2-digit-degree longitudes rejected
  - minutes or seconds ≥ 60
  - out-of-range degrees
  - a hemisphere letter on the wrong axis
  - empty strings
  - decimal numbers written as YAML numbers and as strings
  - surrounding spaces trimmed

  Check exact values (for example `250412.00N` → 25.07 within 1e-9), the `DMS()` round trip, the JSON output (a plain number), and that `receiver.lat`/`lon` accept both formats while existing decimal configs load unchanged.
- **Icons:** the Go and TS icon lists are identical; an unknown name is a config error that lists the valid names.
- **Config:** waypoints load from the inline list and `waypointsFile`; duplicates, unknown icons and invalid coordinates are all reported together with index and name; the example still equals `Default()`.
- **API:** the waypoints JSON and the updated contract fixture.
- **UI** (Vitest):
  - icon selection and the color fallback
  - the label zoom threshold
  - the tooltip text (DMS formatting, distance and bearing)
  - the legend switch is remembered and hidden when the list is empty
  - the layer isn't re-created on snapshots
- `go test -race ./...`, `go vet`, `gofmt`, `go fix`, `npm test`, `npm run lint`, `npm run typecheck` all clean.

## Milestones

1. **Config:** `geo.Latitude`/`geo.Longitude` (parsers, YAML/JSON/text interfaces, `DMS()`), the receiver switched to them, the icon list, `map.waypoints`, optional `map.waypointsFile`, `map.waypointLabelMinZoom`, validation, the example config and `.gitignore`/`.dockerignore`, tests.
2. **API:** waypoints in `/api/map` and the `map` WebSocket message, with types, fixture and contract tests.
3. **Map:** `WaypointLayer`, SVG icons, zoom-dependent labels, tooltips, the legend switch, the 500-point performance check, fictional demo waypoints.
4. **Docs:** a README section (formats with examples, icons, the separate file, the privacy note) and `CLAUDE.md` architecture notes.
