package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/tracker"
	"github.com/PVKonovalov/df17-decoder/web"
)

func uiHandler(ui fs.FS) http.Handler {
	return New(Deps{
		Receiver: Receiver{Lat: 25.07, Lon: 22.87, MaxRangeNm: 300},
		UI:       ui,
	})
}

func serve(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestReceiver(t *testing.T) {
	rec := serve(uiHandler(nil), http.MethodGet, "/api/receiver")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("code %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got, want := strings.TrimSpace(rec.Body.String()), `{"lat":25.07,"lon":22.87,"maxRangeNm":300}`; got != want {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestMapConfig(t *testing.T) {
	h := New(Deps{Map: MapConfig{TileURL: "https://t.lan/{z}/{x}/{y}.png", Attribution: "© me", MaxZoom: 18, DarkFilter: true}})
	rec := serve(h, http.MethodGet, "/api/map")
	want := `{"tileUrl":"https://t.lan/{z}/{x}/{y}.png","attribution":"© me","maxZoom":18,"darkFilter":true,` +
		`"waypointLabelMinZoom":0,"waypoints":[]}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("code %d body %s", rec.Code, rec.Body.String())
	}

	h = New(Deps{Map: MapConfig{WaypointLabelMinZoom: 9, Waypoints: []Waypoint{
		{Name: "ALPHA", Lat: 25.07, Lon: 22.871666666666666, LatDMS: "250412.00N", LonDMS: "0225218.00E", Icon: "star"},
		{Name: "BRAVO", Lat: 25.12, Lon: 22.95, LatDMS: "250712.00N", LonDMS: "0225700.00E", Icon: "diamond", Color: "#f80", Note: "fictional"},
	}}})
	rec = serve(h, http.MethodGet, "/api/map")
	for _, w := range []string{
		`"waypointLabelMinZoom":9`,
		`{"name":"ALPHA","lat":25.07,"lon":22.871666666666666,"latDms":"250412.00N","lonDms":"0225218.00E","icon":"star"}`,
		`{"name":"BRAVO","lat":25.12,"lon":22.95,"latDms":"250712.00N","lonDms":"0225700.00E","icon":"diamond","color":"#f80","note":"fictional"}`,
	} {
		if !strings.Contains(rec.Body.String(), w) {
			t.Errorf("body %s does not contain %s", rec.Body, w)
		}
	}
}

// TestMapContract keeps MapConfig (with a fully populated waypoint) and web/src/api/map.fixture.json in sync.
func TestMapContract(t *testing.T) {
	var w Waypoint
	populate(t, reflect.ValueOf(&w).Elem())
	data, err := json.Marshal(MapConfig{Waypoints: []Waypoint{w}})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../web/src/api/map.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := jsonKeys(t, data), jsonKeys(t, fixture); !slices.Equal(got, want) {
		t.Errorf("MapConfig JSON keys drifted from web/src/api/map.fixture.json\n Go:      %v\n fixture: %v", got, want)
	}
	var g, f struct{ Waypoints []json.RawMessage }
	if json.Unmarshal(data, &g) != nil || json.Unmarshal(fixture, &f) != nil || len(f.Waypoints) == 0 {
		t.Fatal("waypoints missing")
	}
	if got, want := jsonKeys(t, g.Waypoints[0]), jsonKeys(t, f.Waypoints[0]); !slices.Equal(got, want) {
		t.Errorf("Waypoint JSON keys drifted from the fixture\n Go:      %v\n fixture: %v", got, want)
	}
}

func TestUI(t *testing.T) {
	h := uiHandler(fstest.MapFS{
		"index.html":       {Data: []byte("<html>app</html>")},
		"assets/app-1.js":  {Data: []byte("console.log(1)")},
		"favicon.svg":      {Data: []byte("<svg/>")},
		"assets/sub/x.css": {Data: []byte("a{}")},
	})
	tests := []struct {
		method, path string
		code         int
		body         string
		cache        string
	}{
		{"GET", "/", 200, "<html>app</html>", "no-cache"},
		{"GET", "/?icao=738ABC&q=ELY&pos=1&sort=-altitude", 200, "<html>app</html>", "no-cache"},
		{"GET", "/aircraft/738ABC", 200, "<html>app</html>", "no-cache"},
		{"GET", "/assets", 200, "<html>app</html>", "no-cache"},
		{"GET", "/../../etc/passwd", 307, "", ""}, // ServeMux redirects to the cleaned path
		{"GET", "/assets/app-1.js", 200, "console.log(1)", "public, max-age=31536000, immutable"},
		{"GET", "/assets/sub/x.css", 200, "a{}", "public, max-age=31536000, immutable"},
		{"GET", "/favicon.svg", 200, "<svg/>", "no-cache"},
		{"HEAD", "/", 200, "", "no-cache"},
		{"POST", "/", 405, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			rec := serve(h, tt.method, tt.path)
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d", rec.Code, tt.code)
			}
			if tt.code == 200 && rec.Body.String() != tt.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.body)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.cache)
			}
		})
	}
}

func TestUI_APIPathsStayJSON(t *testing.T) {
	h := uiHandler(fstest.MapFS{"index.html": {Data: []byte("app")}})
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, path := range []string{"/api/nope", "/api/aircraft/x/y", "/api/"} {
			rec := serve(h, method, path)
			if rec.Code != http.StatusNotFound || rec.Header().Get("Content-Type") != "application/json" {
				t.Errorf("%s %s: code %d, content type %q", method, path, rec.Code, rec.Header().Get("Content-Type"))
			}
		}
	}
}

func TestUI_EmbeddedDist(t *testing.T) {
	rec := serve(uiHandler(web.Dist()), http.MethodGet, "/some/route")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Fatalf("code %d, body %.80q", rec.Code, rec.Body.String())
	}
}

// populate sets every field of v (a struct pointer) to a non-zero value, recursing through pointers.
func populate(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		populate(t, v.Elem())
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)))
			return
		}
		for _, field := range v.Fields() {
			populate(t, field)
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint64:
		v.SetUint(1)
	case reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		populate(t, k)
		populate(t, e)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		populate(t, s.Index(0))
		v.Set(s)
	default:
		t.Fatalf("populate: unhandled kind %s (%s); extend populate", v.Kind(), v.Type())
	}
}

func jsonKeys(t *testing.T, data []byte) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// TestAircraftContract keeps tracker.Aircraft and the UI fixture (shared with the TypeScript contract test)
// in sync: a fully populated Aircraft must marshal to exactly the fixture's keys.
func TestAircraftContract(t *testing.T) {
	var a tracker.Aircraft
	populate(t, reflect.ValueOf(&a).Elem())
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../web/src/api/aircraft.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := jsonKeys(t, data), jsonKeys(t, fixture); !slices.Equal(got, want) {
		t.Errorf("Aircraft JSON keys drifted from web/src/api/aircraft.fixture.json\n Go:      %v\n fixture: %v", got, want)
	}
}

// TestTrackPointContract keeps tracker.TrackPoint JSON and the UI fixture in sync, like TestAircraftContract.
func TestTrackPointContract(t *testing.T) {
	p := tracker.TrackPoint{T: 1, Lat: 1, Lon: 1, Alt: 1, AltSrc: tracker.AltGNSS, GS: 1, Track: 1, VR: 1,
		Known: tracker.KnownGS | tracker.KnownTrack | tracker.KnownVR, OnGround: true}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../../web/src/api/trackpoint.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := jsonKeys(t, data), jsonKeys(t, fixture); !slices.Equal(got, want) {
		t.Errorf("TrackPoint JSON keys drifted from web/src/api/trackpoint.fixture.json\n Go:      %v\n fixture: %v", got, want)
	}
}

func TestCoverage(t *testing.T) {
	f := newFixture(t)
	rec, _ := f.get(t, "/api/coverage")
	if rec.Code != http.StatusOK {
		t.Fatalf("code %d", rec.Code)
	}
	var s struct {
		Receiver  map[string]float64 `json:"receiver"`
		BucketDeg int                `json:"bucketDeg"`
		Points    []map[string]any   `json:"points"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	// fixture positions, due north and south, so CPR rounding may land them on either side of a bucket edge:
	// 738001 ~10 NM north (0 ft), 738002 ~40 NM south (12000 ft)
	if s.BucketDeg != 2 || s.Receiver["lat"] != 25.07 || len(s.Points) < 2 {
		t.Fatalf("coverage = %s", rec.Body)
	}
	for _, p := range s.Points {
		brg, d, alt := p["bearing"].(float64), p["distanceNm"].(float64), p["alt"].(float64)
		north := (brg < 2 || brg >= 358) && d > 9 && d < 11 && alt == 0
		south := brg > 178 && brg < 182 && d > 39 && d < 41 && alt == 12000
		if !north && !south {
			t.Errorf("unexpected point %v", p)
		}
	}
}

func TestCoverage_EmptyWithoutRecorder(t *testing.T) {
	rec := serve(New(Deps{Receiver: Receiver{Lat: 1, Lon: 2}}), http.MethodGet, "/api/coverage")
	if got := strings.TrimSpace(rec.Body.String()); got != `{"receiver":{"lat":1,"lon":2},"bucketDeg":2,"points":[]}` {
		t.Errorf("body %s", got)
	}
}

func TestCoverageGrid(t *testing.T) {
	rec, _ := newFixture(t).get(t, "/api/coverage/grid")
	var g struct {
		RingNm int `json:"ringNm"`
		Bands  []struct {
			Name  string       `json:"name"`
			Cells [][7]float64 `json:"cells"`
		} `json:"bands"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s (err %v)", rec.Code, rec.Body, err)
	}
	// fixture: 738001 ~10 NM north at 0 ft (low, ring 2), 738002 ~40 NM south at 12000 ft (mid, ring 8)
	if g.RingNm != 5 || len(g.Bands) != 4 || len(g.Bands[0].Cells) == 0 || len(g.Bands[1].Cells) == 0 {
		t.Fatalf("grid %s", rec.Body)
	}
	if c := g.Bands[1].Cells[0]; c[1] != 8 || c[2] < 1 {
		t.Errorf("mid cell %v", c)
	}
}
