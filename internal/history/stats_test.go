package history

import (
	"context"
	"testing"
	"time"
)

func rolledUp(t *testing.T) *Store {
	t.Helper()
	s := rollupStore(t, Options{})
	for h := -1; h <= 1; h++ {
		if err := s.rollupHour(context.Background(), t0.Add(time.Duration(h)*time.Hour), &liveHour{messages: 100 + int64(h)}, sizes{db: new(int64(1000 * (h + 2))), tape: new(int64(5000 * (h + 2)))}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestHourly_AddsThePartialCurrentHour(t *testing.T) {
	s := rolledUp(t)
	ctx := t.Context()
	now := t0.Add(2*time.Hour + 10*time.Minute)
	s.live.boundary(t0.Add(2*time.Hour), Counters{Messages: 500, Frames: 900})
	s.src.Store(&RollupSources{Counters: func() Counters { return Counters{Messages: 540, Frames: 1000} }})

	rows, err := s.Hourly(ctx, t0.Add(-2*time.Hour), now.Add(time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || !rows[0].Hour.Equal(t0.Add(-time.Hour)) || rows[0].Partial || *rows[1].Messages != 100 {
		t.Fatalf("%+v", rows)
	}
	cur := rows[3]
	if !cur.Partial || !cur.Hour.Equal(t0.Add(2*time.Hour)) || cur.AircraftModeS != 1 || cur.AircraftADSB != 0 || cur.Messages == nil || *cur.Messages != 40 || *cur.Frames != 100 {
		t.Errorf("current hour %+v", cur)
	}
	if rows, _ := s.Hourly(ctx, t0, t0.Add(time.Hour), now); len(rows) != 1 || rows[0].Partial {
		t.Errorf("a past range got %+v", rows)
	}
}

func TestCountriesAndOperators(t *testing.T) {
	s := rolledUp(t)
	ctx := t.Context()
	now := t0.Add(5 * time.Hour) // nothing in the current hour
	c, err := s.Countries(ctx, t0.Add(-time.Hour), now, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 738ABC (Israel) in hours -1, 0, 1; 40621D (UK) in 0 and 1; the rest once
	if len(c) != 2 || c[0] != (CountryCount{"Israel", 3}) || c[1] != (CountryCount{"United Kingdom", 2}) {
		t.Errorf("countries %+v", c)
	}
	o, err := s.Operators(ctx, t0.Add(-time.Hour), now, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(o) != 2 || o[0] != (OperatorCount{"DLH", 1}) || o[1] != (OperatorCount{"KLM", 1}) {
		t.Errorf("operators %+v", o)
	}
	live := t0.Add(2*time.Hour + 10*time.Minute) // the current hour adds 738ABC once more
	if c, _ := s.Countries(ctx, t0.Add(-time.Hour), live.Add(time.Hour), live, 1); c[0] != (CountryCount{"Israel", 4}) {
		t.Errorf("with the current hour %+v", c)
	}
}

func TestRangeDaily(t *testing.T) {
	s := rolledUp(t)
	ctx := t.Context()
	now := t0.Add(5 * time.Hour)
	utc, err := s.RangeDaily(ctx, t0.Add(-24*time.Hour), now, now, time.UTC)
	if err != nil || len(utc) != 1 || utc[0].Day != "2026-11-20" || utc[0].MaxRangeNm < 54 || !utc[0].Hour.Equal(t0) {
		t.Fatalf("%+v %v", utc, err)
	}
	// 12:00 and 13:00 UTC are 01:00 and 02:00 the next day in Auckland (UTC+13 in November)
	nz, _ := time.LoadLocation("Pacific/Auckland")
	if d, _ := s.RangeDaily(ctx, t0.Add(-24*time.Hour), now, now, nz); len(d) != 1 || d[0].Day != "2026-11-21" {
		t.Errorf("Auckland %+v", d)
	}
}

func TestSizeSamplesAndOldest(t *testing.T) {
	s := rolledUp(t)
	ctx := t.Context()
	ss, err := s.SizeSamples(ctx, t0)
	if err != nil || len(ss) != 2 || *ss[0].DB != 2000 || *ss[1].Tape != 15000 {
		t.Fatalf("%+v %v", ss, err)
	}
	f, r, err := s.Oldest(ctx)
	if err != nil || !f.Equal(t0.Add(-30*time.Minute)) || !r.Equal(t0.Add(-time.Hour)) {
		t.Errorf("oldest %v %v %v", f, r, err)
	}
	if s.DBBytes() <= 0 {
		t.Error("no database size")
	}
}
