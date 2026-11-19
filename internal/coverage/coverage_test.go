package coverage

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
)

var (
	rx = geo.Point{Lat: 25.07, Lon: 22.87}
	t0 = time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
)

// at returns the point distNm from rx on bearing brg (flat approximation, within 0.2° for these distances).
func at(brg, distNm float64) geo.Point {
	r := brg * math.Pi / 180
	return geo.Point{Lat: rx.Lat + distNm*math.Cos(r)/60, Lon: rx.Lon + distNm*math.Sin(r)/(60*math.Cos(rx.Lat*math.Pi/180))}
}

func TestAdd_KeepsFurthestPerBucket(t *testing.T) {
	c := New(rx)
	c.Add(at(10.6, 50), new(30000), nil, t0)
	c.Add(at(11, 80), nil, nil, t0.Add(time.Second))     // same 10–12° bucket, further
	c.Add(at(11.4, 60), nil, nil, t0.Add(2*time.Second)) // nearer: ignored
	c.Add(at(359.5, 20), nil, nil, t0)                   // last bucket
	c.Add(at(0.5, 30), nil, nil, t0)                     // first bucket

	s := c.Snapshot()
	if s.BucketDeg != 2 || len(s.Points) != 3 {
		t.Fatalf("snapshot %+v", s)
	}
	if p := s.Points[0]; p.Bearing > 2 || p.DistanceNm < 29 || p.DistanceNm > 31 {
		t.Errorf("first bucket %+v", p)
	}
	if p := s.Points[1]; p.DistanceNm < 79 || p.DistanceNm > 81 || p.Alt != nil || !p.Time.Equal(t0.Add(time.Second)) {
		t.Errorf("10° bucket %+v", p)
	}
	if p := s.Points[2]; p.Bearing < 358 {
		t.Errorf("last bucket %+v", p)
	}

	c = New(rx)
	c.Add(at(359.99, 30), nil, nil, t0)
	if b := c.Snapshot().Points[0].Bearing; b != 0 {
		t.Errorf("359.99° rounds to %v, want 0", b)
	}
}

func TestSaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.json")
	c := New(rx)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("an unchanged outline must not be written")
	}
	c.Add(at(90, 120), new(35000), nil, t0)
	c.Add(at(200, 40), nil, nil, t0)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}

	got, discarded, err := Load(path, rx)
	if err != nil || discarded {
		t.Fatal(err, discarded)
	}
	a, b := c.Snapshot(), got.Snapshot()
	if len(b.Points) != 2 || b.Points[0].DistanceNm != a.Points[0].DistanceNm || *b.Points[0].Alt != 35000 || !b.Points[1].Time.Equal(t0) {
		t.Errorf("loaded %+v, saved %+v", b, a)
	}
	if got.dirty {
		t.Error("a freshly loaded outline is not dirty")
	}

	if c, discarded, err := Load(path, geo.Point{Lat: 25.2, Lon: 22.87}); err != nil || !discarded || len(c.Snapshot().Points) != 0 {
		t.Errorf("receiver moved: discarded=%v err=%v points=%d", discarded, err, len(c.Snapshot().Points))
	}
	if c, discarded, err := Load(filepath.Join(t.TempDir(), "none.json"), rx); err != nil || discarded || len(c.Snapshot().Points) != 0 {
		t.Errorf("missing file: %v %v", discarded, err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(path, rx); err == nil {
		t.Error("corrupt file: expected error")
	}
}

func TestSave_ErrorKeepsDirty(t *testing.T) {
	c := New(rx)
	c.Add(at(45, 10), nil, nil, t0)
	if err := c.Save(filepath.Join(t.TempDir(), "missing-dir", "c.json")); err == nil {
		t.Fatal("expected error")
	}
	if !c.dirty {
		t.Error("a failed save must be retried")
	}
}

func TestGrid_CellsAndBands(t *testing.T) {
	c := New(rx)
	c.Add(at(11, 42), new(35000), new(-10.0), t0) // bucket 5, ring 8, high
	c.Add(at(11, 43), new(36000), new(-20.0), t0) // same cell
	c.Add(at(11, 44), new(37000), nil, t0)        // same cell, no signal
	c.Add(at(11, 47), new(35000), new(-30.0), t0) // ring 9
	c.Add(at(181, 3), new(0), new(-3.0), t0)      // bucket 90, ring 0, low
	c.Add(at(181, 3), new(12000), nil, t0)        // mid
	c.Add(at(181, 3), nil, nil, t0)               // unknown altitude

	g := c.Grid()
	if g.RingNm != RingNm || g.BucketDeg != BucketDeg || len(g.Bands) != 4 || g.Bands[2].Name != "high" {
		t.Fatalf("grid %+v", g)
	}
	high := g.Bands[2].Cells
	if len(high) != 2 {
		t.Fatalf("high cells %v", high)
	}
	// mean of linear power: 10·log10((0.1+0.01)/2) = -12.6 dBFS, not the dB mean -15
	if want := (Cell{5, 8, 3, 2, -12.6, -20, -10}); high[0] != want {
		t.Errorf("cell %v, want %v", high[0], want)
	}
	if want := (Cell{5, 9, 1, 1, -30, -30, -30}); high[1] != want {
		t.Errorf("cell %v, want %v", high[1], want)
	}
	if low := g.Bands[0].Cells; len(low) != 1 || low[0] != (Cell{90, 0, 1, 1, -3, -3, -3}) {
		t.Errorf("low %v", low)
	}
	if mid, unk := g.Bands[1].Cells, g.Bands[3].Cells; len(mid) != 1 || len(unk) != 1 || mid[0] != (Cell{90, 0, 1, 0, 0, 0, 0}) {
		t.Errorf("mid %v unknown %v", mid, unk)
	}
}

func TestGrid_SaveLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coverage.json")
	c := New(rx)
	c.Add(at(11, 42), new(35000), new(-10.0), t0)
	c.Add(at(11, 43), new(35000), new(-20.0), t0)
	c.Add(at(300, 120), nil, nil, t0)
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(path, rx)
	if err != nil {
		t.Fatal(err)
	}
	a, b := c.Grid(), got.Grid()
	if len(b.Bands[2].Cells) != 1 || b.Bands[2].Cells[0] != a.Bands[2].Cells[0] || len(b.Bands[3].Cells) != 1 {
		t.Fatalf("loaded %v, saved %v", b.Bands, a.Bands)
	}
	got.Add(at(11, 42), new(35000), new(-10.0), t0) // keeps accumulating on the loaded sums
	if cl := got.Grid().Bands[2].Cells[0]; cl[2] != 3 || cl[3] != 3 || math.Abs(cl[4]-10*math.Log10((0.1+0.01+0.1)/3)) > 0.02 {
		t.Errorf("after load and add: %v", cl)
	}

	// an outline-only file from before the grid still loads
	if err := os.WriteFile(path, []byte(`{"receiver":{"lat":25.07,"lon":22.87},"bucketDeg":2,"points":[{"bearing":10,"distanceNm":50,"lat":25.9,"lon":23,"time":"2026-11-19T12:00:00Z"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old, discarded, err := Load(path, rx)
	if err != nil || discarded || len(old.Snapshot().Points) != 1 || len(old.Grid().Bands[0].Cells) != 0 {
		t.Errorf("old file: %v %v %+v", err, discarded, old.Snapshot())
	}
}
