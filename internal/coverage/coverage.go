// Package coverage keeps the receiver's range outline (the furthest accepted position per bearing bucket) and a
// polar grid of position counts and signal levels per bearing bucket, distance ring and altitude band.
package coverage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
)

const (
	// BucketDeg is the width of one bearing bucket.
	BucketDeg = 2
	buckets   = 360 / BucketDeg
	// RingNm is the depth of one distance ring of the grid.
	RingNm = 5
	// receiverTolerance is how far (degrees) a saved receiver position may differ from the configured one.
	receiverTolerance = 0.001
)

// Point is the furthest position seen in one bearing bucket.
type Point struct {
	Bearing    float64   `json:"bearing"`    // degrees true from the receiver
	DistanceNm float64   `json:"distanceNm"` // from the receiver
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	Alt        *int      `json:"alt,omitempty"` // ft, when known
	Time       time.Time `json:"time"`          // data time of the position
}

// Snapshot is the outline as served by the API and saved to disk: non-empty buckets in bearing order.
type Snapshot struct {
	Receiver  Receiver `json:"receiver"`
	BucketDeg int      `json:"bucketDeg"`
	Points    []Point  `json:"points"`
}

// Receiver is the position the outline was collected around.
type Receiver struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Bands are the grid's altitude bands: below 10,000 ft (and on the ground), 10,000–24,999 ft, 25,000 ft and
// above, and positions without a known altitude.
var Bands = []string{"low", "mid", "high", "unknown"}

func bandOf(alt *int) int {
	switch {
	case alt == nil:
		return 3
	case *alt < 10_000:
		return 0
	case *alt < 25_000:
		return 1
	default:
		return 2
	}
}

// Grid is the polar grid as served by the API: one cell list per altitude band, in the order of Bands.
type Grid struct {
	Receiver  Receiver `json:"receiver"`
	BucketDeg int      `json:"bucketDeg"`
	RingNm    int      `json:"ringNm"`
	Bands     []Band   `json:"bands"`
}

// Band holds the non-empty cells of one altitude band, ordered by bucket, then ring.
type Band struct {
	Name  string `json:"name"`
	Cells []Cell `json:"cells"`
}

// Cell is [bucket, ring, positions, positions with a signal level, average dBFS (mean of linear power),
// min dBFS, max dBFS]. The signal values are 0 when no position had a signal level.
type Cell [7]float64

type cellKey struct{ band, bucket, ring int }

type cell struct {
	n, sn    uint64
	pow      float64 // sum of linear power
	min, max float64 // dBFS
}

// Coverage is safe for one writer (Add) and many readers.
type Coverage struct {
	receiver geo.Point
	mu       sync.RWMutex
	buckets  [buckets]*Point
	cells    map[cellKey]*cell
	dirty    bool
}

// New returns an empty outline and grid around receiver.
func New(receiver geo.Point) *Coverage {
	return &Coverage{receiver: receiver, cells: map[cellKey]*cell{}}
}

// Add records position p (altitude alt in ft and frame signal rssi in dBFS, both may be nil) seen at data time
// t: in its grid cell, and in the outline if it is the furthest so far in its bearing bucket.
func (c *Coverage) Add(p geo.Point, alt *int, rssi *float64, t time.Time) {
	d := geo.DistanceNm(c.receiver, p)
	brg := geo.Bearing(c.receiver, p)
	i := int(brg/BucketDeg) % buckets
	c.mu.Lock()
	defer c.mu.Unlock()
	c.dirty = true

	k := cellKey{bandOf(alt), i, int(d / RingNm)}
	cl := c.cells[k]
	if cl == nil {
		cl = &cell{}
		c.cells[k] = cl
	}
	cl.n++
	if rssi != nil {
		if cl.sn == 0 || *rssi < cl.min {
			cl.min = *rssi
		}
		if cl.sn == 0 || *rssi > cl.max {
			cl.max = *rssi
		}
		cl.sn++
		cl.pow += math.Pow(10, *rssi/10)
	}

	if b := c.buckets[i]; b != nil && b.DistanceNm >= d {
		return
	}
	pt := &Point{Bearing: math.Mod(round(brg, 1), 360), DistanceNm: round(d, 2), Lat: p.Lat, Lon: p.Lon, Time: t}
	if alt != nil {
		pt.Alt = new(*alt)
	}
	c.buckets[i] = pt
}

// Grid returns a copy of the grid.
func (c *Coverage) Grid() Grid {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g := Grid{Receiver: Receiver{Lat: c.receiver.Lat, Lon: c.receiver.Lon}, BucketDeg: BucketDeg, RingNm: RingNm}
	for _, name := range Bands {
		g.Bands = append(g.Bands, Band{Name: name, Cells: []Cell{}})
	}
	for k, cl := range c.cells {
		v := Cell{float64(k.bucket), float64(k.ring), float64(cl.n), float64(cl.sn)}
		if cl.sn > 0 {
			v[4] = round(10*math.Log10(cl.pow/float64(cl.sn)), 2)
			v[5], v[6] = round(cl.min, 1), round(cl.max, 1)
		}
		g.Bands[k.band].Cells = append(g.Bands[k.band].Cells, v)
	}
	for _, b := range g.Bands {
		slices.SortFunc(b.Cells, func(x, y Cell) int {
			if x[0] != y[0] {
				return int(x[0] - y[0])
			}
			return int(x[1] - y[1])
		})
	}
	return g
}

// saved is the file format: the outline and the grid.
type saved struct {
	Snapshot
	RingNm int    `json:"ringNm"`
	Bands  []Band `json:"bands"`
}

// Snapshot returns a copy of the outline.
func (c *Coverage) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := Snapshot{Receiver: Receiver{Lat: c.receiver.Lat, Lon: c.receiver.Lon}, BucketDeg: BucketDeg, Points: []Point{}}
	for _, b := range c.buckets {
		if b != nil {
			s.Points = append(s.Points, *b)
		}
	}
	return s
}

// Save writes the outline to path as JSON (via a temporary file and rename) if it changed since the last save.
func (c *Coverage) Save(path string) error {
	c.mu.Lock()
	dirty := c.dirty
	c.dirty = false
	c.mu.Unlock()
	if !dirty {
		return nil
	}
	g := c.Grid()
	data, err := json.Marshal(saved{Snapshot: c.Snapshot(), RingNm: g.RingNm, Bands: g.Bands})
	if err == nil {
		err = writeFileAtomic(path, data)
	}
	if err != nil {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
	}
	return err
}

// Load returns the outline and grid saved at path for receiver. A missing file gives empty ones. A file saved for
// another receiver position or bucket width is ignored (discarded=true); a grid saved with another ring depth or
// band set is dropped and the outline kept.
func Load(path string, receiver geo.Point) (c *Coverage, discarded bool, err error) {
	c = New(receiver)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, false, nil
	}
	if err != nil {
		return c, false, err
	}
	var s saved
	if err := json.Unmarshal(data, &s); err != nil {
		return c, false, fmt.Errorf("coverage %s: %w", path, err)
	}
	if s.BucketDeg != BucketDeg || math.Abs(s.Receiver.Lat-receiver.Lat) > receiverTolerance ||
		math.Abs(s.Receiver.Lon-receiver.Lon) > receiverTolerance {
		return c, true, nil
	}
	for _, p := range s.Points {
		i := int(p.Bearing/BucketDeg) % buckets
		if b := c.buckets[i]; b == nil || b.DistanceNm < p.DistanceNm {
			c.buckets[i] = &p
		}
	}
	if s.RingNm == RingNm && len(s.Bands) == len(Bands) {
		for bi, b := range s.Bands {
			for _, v := range b.Cells {
				cl := &cell{n: uint64(v[2]), sn: uint64(v[3]), min: v[5], max: v[6]}
				cl.pow = float64(cl.sn) * math.Pow(10, v[4]/10)
				c.cells[cellKey{bi, int(v[0]), int(v[1])}] = cl
			}
		}
	}
	return c, false, nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}
