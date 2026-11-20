package history

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/tracker"
)

func TestTimeline(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	t.Cleanup(func() { s.Close() })
	at := func(min int) time.Time { return t0.Add(time.Duration(min) * time.Minute) }
	s.Flight(tracker.FlightInfo{ID: 1, ICAO: "4840D6", ADSB: true, Callsign: "KLM1023", FirstSeen: at(0), LastSeen: at(2).Add(30 * time.Second), Closed: true})
	s.Flight(tracker.FlightInfo{ID: 2, ICAO: "738ABC", FirstSeen: at(1), LastSeen: at(4), Closed: true})
	s.Sample(2, tracker.Sample{T: at(1).UnixMilli(), Squawk: "7000"})
	s.Sample(2, tracker.Sample{T: at(3).UnixMilli(), Squawk: "7600"})
	s.Sample(2, tracker.Sample{T: at(3).Add(30 * time.Second).UnixMilli(), Squawk: "7600"})
	s.Flight(tracker.FlightInfo{ID: 3, ICAO: "40621D", ADSB: true, Squawk: "7700", FirstSeen: at(2), LastSeen: at(2), Closed: true})
	s.Flight(tracker.FlightInfo{ID: 4, ICAO: "40621E", ADSB: true, FirstSeen: at(30), LastSeen: at(31), Closed: true})
	if _, err := s.write(drain(s)); err != nil {
		t.Fatal(err)
	}

	tl, err := s.Timeline(t.Context(), at(0).Add(10*time.Second), at(5), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := [][2]int{{1, 0}, {1, 1}, {2, 1}, {0, 1}, {0, 1}}
	if !tl.From.Equal(at(0)) || tl.Bucket != "1m0s" || len(tl.Buckets) != len(want) {
		t.Fatalf("%+v", tl)
	}
	for i := range want {
		if tl.Buckets[i] != want[i] {
			t.Errorf("bucket %d = %v, want %v", i, tl.Buckets[i], want[i])
		}
	}
	if len(tl.Events) != 2 || tl.Events[0].FlightID != 3 || tl.Events[0].Squawk != "7700" || !tl.Events[0].T.Equal(at(2)) ||
		tl.Events[1].FlightID != 2 || tl.Events[1].Squawk != "7600" || !tl.Events[1].T.Equal(at(3)) {
		t.Errorf("events %+v", tl.Events)
	}

	if _, err := s.Timeline(t.Context(), at(0), at(0).Add(MaxTimelineBuckets*time.Second+time.Second), time.Second); err == nil {
		t.Error("too many buckets accepted")
	}
	if tl, err := s.Timeline(t.Context(), at(10), at(12), time.Minute); err != nil || tl.Buckets[0] != [2]int{} || len(tl.Events) != 0 {
		t.Errorf("quiet range: %+v %v", tl, err)
	}
}

func TestFlights_Text(t *testing.T) {
	s := knownStore(t)
	for _, c := range []struct {
		text string
		ids  []int64
	}{
		{"klm", []int64{4, 3, 1}}, // callsign prefix, any case (knownStore gives every ADS-B flight KLM1023)
		{"738abc", []int64{2}},    // ICAO
		{"LM", nil},               // not a prefix
		{"%", nil},                // LIKE wildcards are literal
	} {
		list, err := s.Flights(t.Context(), FlightQuery{Text: c.text, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, f := range list {
			ids = append(ids, f.ID)
		}
		if len(ids) != len(c.ids) || (len(ids) > 0 && ids[0] != c.ids[0]) {
			t.Errorf("%q: %v, want %v", c.text, ids, c.ids)
		}
	}
}
