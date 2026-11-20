package tracker

import (
	"encoding/hex"
	"math"
	"slices"
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
	w := &fakeWall{t: t0}
	tr := New(Options{
		TTL:             60 * time.Second,
		KnownAddressTTL: 60 * time.Second,
		Resolver:        resolver(52.0, 4.4),
		Stats:           st,
		Clock:           w.now,
	})
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
	tr.opt.Clock = time.Now // the fake wall is not safe for concurrent use and not needed here
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
		0xA05F21: "United States", 0x3C6589: "Germany", 0x498000: "Czech Republic",
		0x840000: "Japan", 0x87FFFF: "Japan", 0x780000: "China", 0x7BFFFF: "China", 0xE40000: "Brazil",
		0xE7FFFF: "Brazil", 0x7C0000: "Australia", 0x800000: "India", 0x83FFFF: "India", 0xC00000: "Canada",
		0x004000: "Zimbabwe", 0xC97000: "Tuvalu", 0x899000: "Taiwan",
		// territories inside a state's block, and the state around them
		0x789000: "Hong Kong", 0x789FFF: "Hong Kong", 0x788FFF: "China", 0x78A000: "China",
		0x400000: "Bermuda", 0x4001C0: "Cayman Islands", 0x400200: "United Kingdom", 0x43EAFE: "Guernsey",
		// unallocated, and ICAO's own blocks
		0x000001: "", 0x003FFF: "", 0x200000: "", 0x900800: "", 0xB00000: "", 0xF00000: "", 0xFFFFFF: "",
	}
	for icao, want := range tests {
		if got := Country(icao); got != want {
			t.Errorf("Country(%06X) = %q, want %q", icao, got, want)
		}
	}
	if len(countryBlocks) < 190 {
		t.Errorf("%d country blocks, want the full table", len(countryBlocks))
	}
	for _, tbl := range [][]countryBlock{countryBlocks, countrySubBlocks} {
		for i, b := range tbl {
			if b.from > b.to || i > 0 && b.from <= tbl[i-1].to {
				t.Errorf("block %06X (%s) is inverted, unsorted or overlaps the previous one", b.from, b.country)
			}
		}
	}
	for _, b := range countrySubBlocks {
		if parent := lookupBlock(countryBlocks, b.from); parent == "" || parent != lookupBlock(countryBlocks, b.to) {
			t.Errorf("sub-block %06X (%s) is not inside one state's block", b.from, b.country)
		}
	}
}

func TestTracker_SurfacePosition(t *testing.T) {
	tr, _, st := newTestTracker(t)
	// The 1090 MHz Riddle surface vectors: an aircraft taxiing at Schiphol, ~22 NM from the test receiver
	feed(t, tr, t0, "8C4841753AAB238733C8CD4020B1")
	feed(t, tr, t0.Add(time.Second), "8C4841753A9A153237AEF0F275BE")
	a, ok := tr.Get(0x484175)
	if !ok {
		t.Fatal("aircraft missing")
	}
	if !a.HasPosition || a.Lat == nil || math.Abs(*a.Lat-52.32056) > 1e-4 || math.Abs(*a.Lon-4.73574) > 1e-4 {
		t.Errorf("position %v, %v", a.Lat, a.Lon)
	}
	if a.OnGround == nil || !*a.OnGround || a.GroundSpeed == nil || *a.GroundSpeed != 17 || a.Track == nil || *a.Track != 92.8 {
		t.Errorf("onGround=%v gs=%v track=%v", a.OnGround, a.GroundSpeed, a.Track)
	}
	if a.AltBaro != nil {
		t.Errorf("altBaro = %v, want none", *a.AltBaro)
	}
	if got := st.Positions.Load(); got != 2 {
		t.Errorf("positions = %d, want 2", got)
	}
}

func TestTracker_TargetState(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	feed(t, tr, t0, "8DA05629EA21485CBF3F8CADAEEB")
	a, ok := tr.Get(0xA05629)
	if !ok {
		t.Fatal("aircraft missing")
	}
	if a.SelectedAlt == nil || *a.SelectedAlt != 16992 || a.SelectedAltSource != "mcp" || a.BaroSetting == nil || *a.BaroSetting != 1012.8 ||
		a.SelectedHeading == nil || *a.SelectedHeading != 66.8 || !slices.Equal(a.AutopilotModes, []string{"autopilot", "vnav", "lnav", "tcas"}) {
		t.Errorf("alt=%v src=%q baro=%v hdg=%v modes=%v", a.SelectedAlt, a.SelectedAltSource, a.BaroSetting, a.SelectedHeading, a.AutopilotModes)
	}
	snap := tr.Snapshot()[0]
	snap.AutopilotModes[0] = "changed"
	if b, _ := tr.Get(0xA05629); b.AutopilotModes[0] != "autopilot" {
		t.Error("snapshot shares the autopilot modes slice")
	}
}

func commBMsg(icao uint32, at time.Time, c *modes.CommB) modes.Message {
	return modes.Message{DF: 20, ICAO: icao, Time: at, Surveillance: &modes.Surveillance{CommB: c}}
}

func TestTracker_CommBModeSOnly(t *testing.T) {
	tr, _, st := newTestTracker(t)
	const icao = 0x738ABC
	tr.Update(commBMsg(icao, t0, &modes.CommB{BDS40: &modes.BDS40{FMSAltitude: new(35008), BaroSetting: new(1013.2)}}), nil)
	tr.Update(commBMsg(icao, t0, &modes.CommB{BDS50: &modes.BDS50{Roll: new(-12.3), Track: new(90.2), GroundSpeed: new(420.0), TAS: new(440)}}), nil)
	tr.Update(commBMsg(icao, t0, &modes.CommB{BDS60: &modes.BDS60{Heading: new(85.0), IAS: new(280), Mach: new(0.784), VerticalRateINS: new(-1024)}}), nil)
	a, _ := tr.Get(icao)
	if *a.SelectedAlt != 35008 || a.SelectedAltSource != "fms" || *a.BaroSetting != 1013.2 {
		t.Errorf("4,0: alt=%v src=%q baro=%v", a.SelectedAlt, a.SelectedAltSource, a.BaroSetting)
	}
	if *a.Roll != -12.3 || *a.Track != 90.2 || *a.GroundSpeed != 420 || *a.TAS != 440 {
		t.Errorf("5,0: roll=%v trk=%v gs=%v tas=%v", *a.Roll, *a.Track, *a.GroundSpeed, *a.TAS)
	}
	if *a.Heading != 85 || *a.IAS != 280 || *a.Mach != 0.784 || *a.VerticalRate != -1024 {
		t.Errorf("6,0: hdg=%v ias=%v mach=%v vr=%v", *a.Heading, *a.IAS, *a.Mach, *a.VerticalRate)
	}

	// a mis-inferred 5,0 right after: 200 kt off the previous report
	tr.Update(commBMsg(icao, t0.Add(5*time.Second), &modes.CommB{BDS50: &modes.BDS50{GroundSpeed: new(220.0), Track: new(91.0)}}), nil)
	// and a 6,0 with the opposite vertical rate
	tr.Update(commBMsg(icao, t0.Add(5*time.Second), &modes.CommB{BDS60: &modes.BDS60{VerticalRateBaro: new(2048)}}), nil)
	tr.Update(commBMsg(icao, t0.Add(5*time.Second), &modes.CommB{Ambiguous: true}), nil)
	if a, _ := tr.Get(icao); *a.GroundSpeed != 420 || *a.VerticalRate != -1024 {
		t.Errorf("rejected reports applied: gs=%v vr=%v", *a.GroundSpeed, *a.VerticalRate)
	}
	if st.CommBRejected.Load() != 2 || st.CommBAmbiguous.Load() != 1 {
		t.Errorf("rejected=%d ambiguous=%d", st.CommBRejected.Load(), st.CommBAmbiguous.Load())
	}

	// once the reference is older than 30 s, a new report is accepted again
	tr.Update(commBMsg(icao, t0.Add(40*time.Second), &modes.CommB{BDS50: &modes.BDS50{GroundSpeed: new(220.0)}}), nil)
	if a, _ := tr.Get(icao); *a.GroundSpeed != 220 {
		t.Errorf("stale reference: gs=%v, want 220", *a.GroundSpeed)
	}
}

func TestTracker_CommBWithADSB(t *testing.T) {
	tr, _, st := newTestTracker(t)
	feed(t, tr, t0, frameVelocity1) // 485020: GS 159.2 kt, track 182.9°, VR -832 ft/min
	before, _ := tr.Get(0x485020)

	tr.Update(commBMsg(0x485020, t0.Add(time.Second), &modes.CommB{BDS50: &modes.BDS50{Roll: new(1.4), Track: new(185.0), GroundSpeed: new(170.0), TAS: new(180)}}), nil)
	a, _ := tr.Get(0x485020)
	if *a.GroundSpeed != *before.GroundSpeed || *a.Track != *before.Track || *a.Roll != 1.4 || *a.TAS != 180 {
		t.Errorf("ADS-B must own GS/track; Comm-B adds roll and TAS: gs=%v trk=%v roll=%v tas=%v", *a.GroundSpeed, *a.Track, *a.Roll, *a.TAS)
	}

	tr.Update(commBMsg(0x485020, t0.Add(time.Second), &modes.CommB{BDS50: &modes.BDS50{Roll: new(20.0), Track: new(300.0), GroundSpeed: new(160.0)}}), nil)
	tr.Update(commBMsg(0x485020, t0.Add(time.Second), &modes.CommB{BDS60: &modes.BDS60{IAS: new(160), VerticalRateBaro: new(-800)}}), nil)
	tr.Update(commBMsg(0x485020, t0.Add(2*time.Second), &modes.CommB{BDS60: &modes.BDS60{IAS: new(250)}}), nil)
	if a, _ := tr.Get(0x485020); *a.Roll != 1.4 || *a.IAS != 160 || *a.VerticalRate != -832 {
		t.Errorf("inconsistent reports applied or ADS-B VR overridden: roll=%v ias=%v vr=%v", *a.Roll, *a.IAS, *a.VerticalRate)
	}
	if st.CommBRejected.Load() != 2 {
		t.Errorf("rejected = %d, want 2", st.CommBRejected.Load())
	}
}

func TestTracker_CommBAmbiguousResolvedByReference(t *testing.T) {
	tr, _, st := newTestTracker(t)
	const icao = 0x738ABC
	tr.Update(commBMsg(icao, t0, &modes.CommB{BDS50: &modes.BDS50{Track: new(90.0), GroundSpeed: new(120.0)}}), nil)

	// fits 5,0 (consistent with the last report) and 6,0 (no reference yet): taken as 5,0
	both := func(gs float64) *modes.CommB {
		return &modes.CommB{Ambiguous: true, BDS50: &modes.BDS50{Track: new(92.0), GroundSpeed: new(gs), Roll: new(3.0)},
			BDS60: &modes.BDS60{Heading: new(10.0), IAS: new(400)}}
	}
	tr.Update(commBMsg(icao, t0.Add(2*time.Second), both(124)), nil)
	if a, _ := tr.Get(icao); *a.GroundSpeed != 124 || *a.Roll != 3 || a.IAS != nil {
		t.Errorf("resolved as 5,0: gs=%v roll=%v ias=%v", *a.GroundSpeed, *a.Roll, a.IAS)
	}
	// neither candidate agrees: dropped
	tr.Update(commBMsg(icao, t0.Add(4*time.Second), both(300)), nil)
	if a, _ := tr.Get(icao); *a.GroundSpeed != 124 || st.CommBAmbiguous.Load() != 1 {
		t.Errorf("unresolved: gs=%v ambiguous=%d", *a.GroundSpeed, st.CommBAmbiguous.Load())
	}
	// no reference at all: dropped
	tr.Update(commBMsg(0x738ABD, t0, both(124)), nil)
	if a, _ := tr.Get(0x738ABD); a.GroundSpeed != nil || st.CommBAmbiguous.Load() != 2 {
		t.Errorf("no reference: gs=%v ambiguous=%d", a.GroundSpeed, st.CommBAmbiguous.Load())
	}
}

func TestTracker_RSSIAveragesLastFrames(t *testing.T) {
	tr, _, _ := newTestTracker(t)
	b, _ := hex.DecodeString(frameKLMIdent)
	m, _ := modes.Decode(b, t0, modes.Options{})
	rssi := func(v float64) float64 {
		tr.Update(m, &v)
		a, _ := tr.Get(0x4840D6)
		return *a.RSSI
	}
	if got := rssi(-10); got != -10 {
		t.Errorf("first frame: %v", got)
	}
	if got := rssi(-20); got != -12.6 { // 10·log10((0.1 + 0.01) / 2)
		t.Errorf("two frames: %v, want -12.6", got)
	}
	for range rssiWindow {
		rssi(-30)
	}
	if got := rssi(-30); got != -30 {
		t.Errorf("after the window is full of -30: %v", got)
	}
	tr.Update(m, nil)
	if a, _ := tr.Get(0x4840D6); *a.RSSI != -30 {
		t.Errorf("frame without RSSI changed it: %v", *a.RSSI)
	}
}
