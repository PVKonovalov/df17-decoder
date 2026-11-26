package history

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// dailyRows reads daily_aircraft as "day icao" → adsb.
func dailyRows(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.db.Query(`SELECT day, icao, adsb FROM daily_aircraft`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	m := map[string]bool{}
	for rows.Next() {
		var day, icao string
		var adsb bool
		if err := rows.Scan(&day, &icao, &adsb); err != nil {
			t.Fatal(err)
		}
		m[day+" "+icao] = adsb
	}
	return m
}

// days counts the rows per day.
func days(rows map[string]bool) map[string]int {
	m := map[string]int{}
	for k := range rows {
		m[k[:10]]++
	}
	return m
}

func rollupHours(t *testing.T, s *Store, from, to time.Time) {
	t.Helper()
	for h := from; h.Before(to); h = h.Add(time.Hour) {
		if err := s.rollupHour(context.Background(), h, nil, sizes{}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDaily_HourlyRollup(t *testing.T) {
	s := rollupStore(t, Options{})
	rollupHours(t, s, t0.Add(-time.Hour), t0.Add(3*time.Hour))
	want := map[string]bool{
		"2026-11-20 4840D6": true,  // two flights in two hours: one row
		"2026-11-20 738ABC": false, // Mode-S over four hours
		"2026-11-20 40621D": true,
		"2026-11-20 3C6444": true, // a Mode-S and an ADS-B flight: OR-ed
	}
	if got := dailyRows(t, s); !maps.Equal(got, want) {
		t.Errorf("daily %v, want %v", got, want)
	}

	// a later Mode-S-only hour doesn't take ADS-B back
	s.Flight(fl(10, "4840D6", "", false, t0.Add(5*time.Hour), 10*time.Minute, true))
	writeAll(t, s)
	rollupHours(t, s, t0.Add(5*time.Hour), t0.Add(6*time.Hour))
	if !dailyRows(t, s)["2026-11-20 4840D6"] {
		t.Error("adsb was cleared by a Mode-S flight")
	}
}

func TestDaily_MidnightAndHalfHourZone(t *testing.T) {
	midnight := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{})
	defer s.Close()
	s.Flight(fl(1, "AB12CD", "FAKE101", true, midnight.Add(-30*time.Minute), time.Hour, true))
	writeAll(t, s)
	rollupHours(t, s, midnight.Add(-time.Hour), midnight.Add(time.Hour))
	if got := days(dailyRows(t, s)); !maps.Equal(got, map[string]int{"2026-11-20": 1, "2026-11-21": 1}) {
		t.Errorf("a flight across midnight: %v", got)
	}

	// 18:00–19:00 UTC is 23:30–00:30 in India: one UTC hour on two local days
	kolkata, _ := time.LoadLocation("Asia/Kolkata")
	k := open(t, filepath.Join(t.TempDir(), "h.db"), Options{Location: kolkata})
	defer k.Close()
	hour := time.Date(2026, 11, 20, 18, 0, 0, 0, time.UTC)
	k.Flight(fl(1, "C0FFEE", "", false, hour.Add(10*time.Minute), 40*time.Minute, true))
	k.Flight(fl(2, "123456", "", true, hour.Add(5*time.Minute), 10*time.Minute, true)) // 23:35–23:45 IST
	writeAll(t, k)
	rollupHours(t, k, hour, hour.Add(time.Hour))
	want := map[string]bool{"2026-11-20 C0FFEE": false, "2026-11-21 C0FFEE": false, "2026-11-20 123456": true}
	if got := dailyRows(t, k); !maps.Equal(got, want) {
		t.Errorf("Kolkata %v, want %v", got, want)
	}
}

// TestDaily_DST puts one aircraft in every UTC hour around the two DST changes of 2026 in Berlin: the local
// 29 March has 23 hours and 25 October 25, by the hourly rollup and by the build alike.
func TestDaily_DST(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	for _, c := range []struct {
		day         string
		start       time.Time // local midnight in UTC
		hours, want int
	}{
		{"2026-03-29", time.Date(2026, 3, 28, 23, 0, 0, 0, time.UTC), 23, 23},
		{"2026-10-25", time.Date(2026, 10, 24, 22, 0, 0, 0, time.UTC), 25, 25},
	} {
		s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{Location: berlin})
		// one hour before and after the local day too
		for i := -1; i <= c.hours; i++ {
			at := c.start.Add(time.Duration(i)*time.Hour + 20*time.Minute)
			s.Flight(fl(int64(i+2), "A0"+string(rune('A'+i+1))+"000", "", true, at, 10*time.Minute, true))
		}
		writeAll(t, s)
		rollupHours(t, s, c.start.Add(-time.Hour), c.start.Add(time.Duration(c.hours+1)*time.Hour))
		hourly := days(dailyRows(t, s))
		if hourly[c.day] != c.want || len(hourly) != 3 {
			t.Errorf("%s by the hourly rollup: %v, want %d aircraft that day", c.day, hourly, c.want)
		}
		if _, err := s.db.Exec(`DELETE FROM daily_aircraft`); err != nil {
			t.Fatal(err)
		}
		if err := s.buildDaily(context.Background()); err != nil {
			t.Fatal(err)
		}
		if built := days(dailyRows(t, s)); !maps.Equal(built, hourly) {
			t.Errorf("%s built %v, hourly %v", c.day, built, hourly)
		}
		s.Close()
	}
}

func TestDaily_BuildAndTimezoneChange(t *testing.T) {
	file := filepath.Join(t.TempDir(), "h.db")
	s := open(t, file, Options{})
	day := 24 * time.Hour
	s.Flight(fl(1, "AB12CD", "FAKE101", true, t0.Add(-3*day), time.Hour, true))                           // 17 Nov, deleted below
	s.Flight(fl(2, "C0FFEE", "", false, t0.Add(-2*day+11*time.Hour+30*time.Minute), time.Hour, true))     // 18 Nov 23:30–00:30 UTC
	s.Flight(fl(3, "123456", "FAKE300", true, t0.Add(11*time.Hour+30*time.Minute), 10*time.Minute, true)) // 20 Nov 23:30 UTC
	writeAll(t, s)
	if err := s.buildDaily(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"2026-11-17 AB12CD": true, "2026-11-18 C0FFEE": false, "2026-11-19 C0FFEE": false,
		"2026-11-20 123456": true}
	if got := dailyRows(t, s); !maps.Equal(got, want) {
		t.Errorf("built %v, want %v", got, want)
	}
	if tz, _, _ := s.meta(context.Background(), dailyTZKey); tz != "UTC" {
		t.Errorf("daily_tz %q", tz)
	}
	if from, _ := s.DailyExactFrom(context.Background()); from != "" {
		t.Errorf("exact from %q with every flight stored", from)
	}
	if _, err := s.db.Exec(`DELETE FROM flights WHERE id = 1`); err != nil { // as flight retention does
		t.Fatal(err)
	}
	s.Close()

	// in Tokyo (UTC+9) the flights of 18 Nov 23:30 and 20 Nov 23:30 UTC fall on the next days; the oldest stored
	// flight is on 19 Nov there (maybe a partial day), so days before 20 Nov keep their UTC rows
	tokyo, _ := time.LoadLocation("Asia/Tokyo")
	s = open(t, file, Options{Location: tokyo})
	defer s.Close()
	if err := s.buildDaily(context.Background()); err != nil {
		t.Fatal(err)
	}
	want = map[string]bool{"2026-11-17 AB12CD": true, "2026-11-18 C0FFEE": false, "2026-11-19 C0FFEE": false,
		"2026-11-21 123456": true}
	if got := dailyRows(t, s); !maps.Equal(got, want) {
		t.Errorf("rebuilt %v, want %v", got, want)
	}
	if from, _ := s.DailyExactFrom(context.Background()); from != "2026-11-20" {
		t.Errorf("exact from %q, want 2026-11-20", from)
	}
	if tz, _, _ := s.meta(context.Background(), dailyTZKey); tz != "Asia/Tokyo" {
		t.Errorf("daily_tz %q", tz)
	}
	n := len(dailyRows(t, s))
	if err := s.buildDaily(context.Background()); err != nil || len(dailyRows(t, s)) != n {
		t.Errorf("a second build changed something (%v)", err)
	}
}

func TestDaily_BuildDoesNotBlock(t *testing.T) {
	s := rollupStore(t, Options{Now: func() time.Time { return t0.Add(3*time.Hour + 30*time.Minute) }})
	release := make(chan struct{})
	testDailyChunk = func() { <-release }
	t.Cleanup(func() { testDailyChunk = nil })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunRollups(ctx, RollupSources{})
	}()
	wait := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatal(what)
			}
		}
	}
	wait("the hourly backfill waited for the daily build", func() bool {
		_, ok := readHour(t, s, t0.Add(2*time.Hour))
		return ok
	})
	s.Flight(fl(20, "AB12CD", "", true, t0, time.Minute, true))
	writeAll(t, s)
	close(release)
	wait("the daily build did not finish", func() bool {
		tz, _, _ := s.meta(context.Background(), dailyTZKey)
		return tz == "UTC"
	})
	if _, ok := dailyRows(t, s)["2026-11-20 AB12CD"]; !ok {
		t.Error("a flight written during the build is missing")
	}
	cancel()
	<-done
}

func TestDaily_Retention(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "h.db"), Options{StatsRetention: 48 * time.Hour})
	defer s.Close()
	for i := range 5 {
		s.Flight(fl(int64(i+1), "AB12CD", "", true, t0.Add(-time.Duration(i)*24*time.Hour), time.Minute, true))
	}
	writeAll(t, s)
	if err := s.buildDaily(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.pruneRollups(t0.Add(time.Hour)); err != nil { // keeps 18 Nov, which holds the cut at 13:00
		t.Fatal(err)
	}
	got := slices.Sorted(maps.Keys(days(dailyRows(t, s))))
	if !slices.Equal(got, []string{"2026-11-18", "2026-11-19", "2026-11-20"}) {
		t.Errorf("kept %v", got)
	}
}

func TestTZKey(t *testing.T) {
	if k := tzKey(time.UTC, 2026); k != "UTC" {
		t.Error(k)
	}
	if k := tzKey(time.Local, 2026); len(k) < len("Local ") || k[:6] != "Local " {
		t.Errorf("local key %q", k)
	}
}

// dailyStore holds, in UTC, with now = 30 Nov 12:30:
//
//	AB12CD ADS-B   20 Nov 10:00 (the oldest sighting), back on 30 Nov 10:00 as FAKE102
//	C0FFEE Mode-S  30 Nov 09:00, new
//	123456 ADS-B   30 Nov 12:10–12:25, open and not rolled up yet: new, FAKE300
//	ABCDEF Mode-S  29 Nov 23:30 – 30 Nov 00:30, then ADS-B on 30 Nov 08:00
var dNow = time.Date(2026, 11, 30, 12, 30, 0, 0, time.UTC)

func dailyStore(t *testing.T, opt Options) *Store {
	t.Helper()
	opt.Now = func() time.Time { return dNow }
	s := open(t, filepath.Join(t.TempDir(), "h.db"), opt)
	t.Cleanup(func() { s.Close() })
	d := func(day, h, m int) time.Time { return time.Date(2026, 11, day, h, m, 0, 0, time.UTC) }
	s.Flight(fl(1, "AB12CD", "FAKE101", true, d(20, 10, 0), 30*time.Minute, true))
	s.Flight(fl(2, "AB12CD", "FAKE102", true, d(30, 10, 0), 30*time.Minute, true))
	s.Flight(fl(3, "C0FFEE", "", false, d(30, 9, 0), 10*time.Minute, true))
	s.Flight(fl(4, "ABCDEF", "", false, d(29, 23, 30), time.Hour, true))
	s.Flight(fl(5, "ABCDEF", "FAKE500", true, d(30, 8, 0), time.Hour, true))
	writeAll(t, s)
	if err := s.buildDaily(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Flight(fl(6, "123456", "FAKE300", true, d(30, 12, 10), 15*time.Minute, false))
	writeAll(t, s)
	return s
}

func TestDaily_Read(t *testing.T) {
	s := dailyStore(t, Options{StatsRetention: 48 * time.Hour})
	if err := s.pruneRollups(dNow); err != nil {
		t.Fatal(err)
	}
	got, err := s.Daily(context.Background(), dNow.Add(-24*time.Hour), dNow, dNow)
	if err != nil {
		t.Fatal(err)
	}
	want := []DailyRow{
		{Day: "2026-11-29", Aircraft: 1, ModeS: 1, New: 1, NewModeS: 1},
		// AB12CD was away longer than stats.retention and is still not new; ABCDEF is ADS-B that day
		{Day: "2026-11-30", Aircraft: 4, ADSB: 3, ModeS: 1, New: 2, NewADSB: 1, NewModeS: 1, Returning: 2, Partial: true},
	}
	if got.Timezone != "UTC" || !slices.Equal(got.Days, want) {
		t.Errorf("daily %+v\n want %+v", got, want)
	}

	s = dailyStore(t, Options{})
	if _, err := s.db.Exec(`INSERT INTO meta(key, value) VALUES(?, '2026-11-24')`, dailyExactFromKey); err != nil {
		t.Fatal(err)
	}
	all, err := s.Daily(context.Background(), dNow.Add(-40*24*time.Hour), dNow, dNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Days) != 11 || all.Days[0].Day != "2026-11-20" || all.Days[0].New != 1 {
		t.Fatalf("from the oldest sighting: %+v", all.Days)
	}
	for _, r := range all.Days {
		if r.Learning != (r.Day < "2026-11-27") || r.Approximate != (r.Day < "2026-11-24") || r.Partial != (r.Day == "2026-11-30") {
			t.Errorf("flags of %+v", r)
		}
		if r.Day > "2026-11-20" && r.Day < "2026-11-29" && r.Aircraft != 0 {
			t.Errorf("a quiet day %+v", r)
		}
	}
	empty := open(t, filepath.Join(t.TempDir(), "e.db"), Options{})
	defer empty.Close()
	if e, err := empty.Daily(context.Background(), dNow.Add(-time.Hour), dNow, dNow); err != nil || len(e.Days) != 0 {
		t.Errorf("empty %+v %v", e, err)
	}
}

func TestNewOn(t *testing.T) {
	s := dailyStore(t, Options{})
	got, err := s.NewOn(context.Background(), "2026-11-30", 200)
	if err != nil {
		t.Fatal(err)
	}
	want := []NewAircraft{
		{ICAO: "C0FFEE", FirstSeen: time.Date(2026, 11, 30, 9, 0, 0, 0, time.UTC), Country: country("C0FFEE")},
		{ICAO: "123456", Callsign: "FAKE300", FirstSeen: time.Date(2026, 11, 30, 12, 10, 0, 0, time.UTC), Country: country("123456"), ADSB: true},
	}
	if !slices.Equal(got, want) {
		t.Errorf("new %+v\n want %+v", got, want)
	}
	if one, _ := s.NewOn(context.Background(), "2026-11-30", 1); len(one) != 1 || one[0].ICAO != "C0FFEE" {
		t.Errorf("limit 1: %+v", one)
	}
	if first, _ := s.NewOn(context.Background(), "2026-11-29", 10); len(first) != 1 || first[0].ICAO != "ABCDEF" || first[0].ADSB {
		t.Errorf("29 Nov: %+v", first)
	}
	if _, err := s.NewOn(context.Background(), "30.11.2026", 10); err == nil {
		t.Error("a bad day was accepted")
	}
}
