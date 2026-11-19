package tracker

import (
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
	"github.com/PVKonovalov/df17-decoder/internal/stats"
)

const (
	frameKLMIdent  = "8D4840D6202CC371C32CE0576098"
	frameVelocity1 = "8D485020994409940838175B284F"
	frameVelocity3 = "8DA05F219B06B6AF189400CBC33F"
	frameDF11      = "5D738ABCF3536C" // 738ABC all-call, IID 0
	frameDF5       = "2800011C5D5770" // 738ABC squawk 0243
	frameDF4       = "2000021CEF5F61" // 738ABC 2500 ft
	frameDF5_7700  = "28000AAA392E75" // 738ABC squawk 7700
	frameDF4_KLM   = "2000183859C38D" // 4840D6 38000 ft
)

type fakeWall struct{ t time.Time }

func (w *fakeWall) now() time.Time { return w.t }

func newTestTracker(t *testing.T) (*Tracker, *fakeWall, *stats.Stats) {
	t.Helper()
	st := stats.New(t0)
	tr := New(Options{
		TTL:             60 * time.Second,
		KnownAddressTTL: 60 * time.Second,
		Resolver:        resolver(52.0, 4.4),
		Stats:           st,
	})
	w := &fakeWall{t: t0}
	tr.wall = w.now
	return tr, w, st
}

func feed(t *testing.T, tr *Tracker, at time.Time, frames ...string) []bool {
	t.Helper()
	var accepted []bool
	for _, f := range frames {
		b, err := hex.DecodeString(f)
		if err != nil {
			t.Fatal(err)
		}
		m, err := modes.Decode(b, at, modes.Options{})
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		accepted = append(accepted, tr.Update(m, nil))
	}
	return accepted
}

func TestTracker_ADSBFields(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	feed(t, tr, t0, frameKLMIdent)
	feed(t, tr, t0, "8D40621D58C386435CC412692AD6")
	feed(t, tr, t0.Add(3*time.Second), "8D40621D58C382D690C8AC2863A7")
	feed(t, tr, t0.Add(3*time.Second), frameVelocity1, frameVelocity3)

	klm, ok := tr.Get(0x4840D6)
	if !ok || klm.Callsign != "KLM1023" || klm.Category != "A0" || !klm.ADSB || klm.Country != "Netherlands" || klm.HasPosition {
		t.Errorf("KLM = %+v", klm)
	}

	a, _ := tr.Get(0x40621D)
	if !a.HasPosition || a.Lat == nil || !near(geo.Point{Lat: *a.Lat, Lon: *a.Lon}, 52.2572, 3.9194) {
		t.Errorf("40621D position = %v %v", a.Lat, a.Lon)
	}
	if a.AltBaro == nil || *a.AltBaro != 38000 || a.DistanceNm == nil || a.Bearing == nil || a.PosTime == nil {
		t.Errorf("40621D = %+v", a)
	}
	if a.Messages != 2 || a.MessagesByDF[17] != 2 || !a.FirstSeen.Equal(t0) || !a.LastSeen.Equal(t0.Add(3*time.Second)) {
		t.Errorf("counts/times = %d %v %v %v", a.Messages, a.MessagesByDF, a.FirstSeen, a.LastSeen)
	}

	v1, _ := tr.Get(0x485020)
	if v1.GroundSpeed == nil || *v1.GroundSpeed != 159.2 || *v1.Track != 182.9 || *v1.VerticalRate != -832 {
		t.Errorf("485020 = %+v", v1)
	}
	v3, _ := tr.Get(0xA05F21)
	if v3.Heading == nil || *v3.Heading != 243.98 || v3.TAS == nil || *v3.TAS != 375 || v3.IAS != nil || v3.Country != "United States" {
		t.Errorf("A05F21 = %+v", v3)
	}
}

func TestTracker_AddressParityNeedsKnownAddress(t *testing.T) {
	tr, _, st := newTestTracker(t)

	if got := feed(t, tr, t0, frameDF5); got[0] {
		t.Fatal("DF5 from unknown address accepted")
	}
	if st.AddressParityDrops.Load() != 1 || tr.Len() != 0 {
		t.Errorf("drops=%d len=%d, want 1, 0", st.AddressParityDrops.Load(), tr.Len())
	}

	got := feed(t, tr, t0.Add(time.Second), frameDF11, frameDF5, frameDF4)
	if !got[0] || !got[1] || !got[2] {
		t.Fatalf("accepted = %v", got)
	}
	a, _ := tr.Get(0x738ABC)
	if a.ADSB || a.HasPosition || a.Squawk != "0243" || a.AltBaro == nil || *a.AltBaro != 2500 ||
		a.Country != "Israel" || a.OnGround == nil || *a.OnGround || a.MessagesByDF[11] != 1 || a.MessagesByDF[5] != 1 {
		t.Errorf("738ABC = %+v", a)
	}

	// known address expires after KnownAddressTTL
	if got := feed(t, tr, t0.Add(62*time.Second), frameDF5); got[0] {
		t.Error("DF5 accepted after known-address TTL")
	}
}

func TestTracker_SquawkEmergency(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	feed(t, tr, t0, frameDF11, frameDF5_7700)
	if a, _ := tr.Get(0x738ABC); a.Emergency != "general" || a.Squawk != "7700" {
		t.Errorf("after 7700: emergency=%q squawk=%q", a.Emergency, a.Squawk)
	}
	feed(t, tr, t0, frameDF5)
	if a, _ := tr.Get(0x738ABC); a.Emergency != "" {
		t.Errorf("after 0243: emergency=%q, want cleared", a.Emergency)
	}
}

func TestTracker_ExpiryFollowsDataTime(t *testing.T) {
	tr, wall, _ := newTestTracker(t)

	// fast replay: frames 100 s apart in capture time arrive at the same wall instant
	feed(t, tr, t0, frameKLMIdent)
	feed(t, tr, t0.Add(100*time.Second), frameVelocity1)

	if _, ok := tr.Get(0x4840D6); ok {
		t.Error("4840D6 last heard 100 s ago in capture time must be expired")
	}
	if _, ok := tr.Get(0x485020); !ok {
		t.Error("485020 must be visible")
	}
	if n := tr.Expire(); n != 1 || tr.Len() != 1 {
		t.Errorf("Expire removed %d, len %d; want 1, 1", n, tr.Len())
	}

	// no more frames: data time keeps running with the wall clock
	wall.t = t0.Add(59 * time.Second)
	if len(tr.Snapshot()) != 1 {
		t.Error("485020 must still be visible 59 s later")
	}
	wall.t = t0.Add(61 * time.Second)
	if len(tr.Snapshot()) != 0 || tr.Expire() != 1 || tr.Len() != 0 {
		t.Error("485020 must expire 61 s after its last frame")
	}
}

func TestTracker_SnapshotIsACopy(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	feed(t, tr, t0, frameKLMIdent)

	snap := tr.Snapshot()
	snap[0].MessagesByDF[17] = 999
	snap[0].Callsign = "XXX"
	a, _ := tr.Get(0x4840D6)
	if a.MessagesByDF[17] != 1 || a.Callsign != "KLM1023" {
		t.Errorf("snapshot aliases tracker state: %+v", a)
	}
}

func TestTracker_ConcurrentReaders(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	tr.wall = time.Now // the fake wall is not safe for concurrent use and not needed here
	frames := []string{frameKLMIdent, frameVelocity1, frameVelocity3, frameDF11, frameDF5, frameDF4, frameDF4_KLM}

	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 500 {
			feed(t, tr, time.Now(), frames[i%len(frames)])
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 500 {
				for _, a := range tr.Snapshot() {
					_ = a.MessagesByDF[17]
				}
				tr.Get(0x4840D6)
				tr.Expire()
			}
		})
	}
	wg.Wait()
	if tr.Len() != 4 { // 4840D6, 485020, A05F21, 738ABC
		t.Errorf("len = %d, want 4", tr.Len())
	}
}

func TestCountry(t *testing.T) {
	tests := map[uint32]string{
		0x738000: "Israel", 0x73FFFF: "Israel", 0x738062: "Israel", 0x740000: "Jordan", 0x744828: "Jordan",
		0x155C06: "Russia", 0x471F65: "Hungary", 0x896539: "United Arab Emirates", 0x4840D6: "Netherlands",
		0xA05F21: "United States", 0x3C6589: "Germany", 0x000001: "",
	}
	for icao, want := range tests {
		if got := Country(icao); got != want {
			t.Errorf("Country(%06X) = %q, want %q", icao, got, want)
		}
	}
	for i, b := range countryBlocks {
		if b.from > b.to || i > 0 && b.from <= countryBlocks[i-1].to {
			t.Errorf("block %d (%s) is inverted or overlaps the previous one", i, b.country)
		}
	}
}
