package tracker

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"
)

const (
	// trailTurnDeg and trailAltFt keep an extra trail point in a turn or a climb before minInterval passes.
	trailTurnDeg = 3.0
	trailAltFt   = 200

	defaultTrailWindow      = 10 * time.Minute
	defaultTrailMinInterval = 5 * time.Second
)

// AltSource says where TrackPoint.Alt comes from.
type AltSource uint8

const (
	AltUnknown AltSource = iota
	AltBaro
	AltGNSS
)

// Known flags the optional TrackPoint values that are set.
type Known uint8

const (
	KnownGS Known = 1 << iota
	KnownTrack
	KnownVR
)

// TrackPoint is one thinned trail point; T is unix ms in data time.
type TrackPoint struct {
	T        int64
	Lat, Lon float64
	Alt      int // ft, valid unless AltSrc is AltUnknown
	AltSrc   AltSource
	GS       float64 // kt, valid with KnownGS
	Track    float64 // degrees true, valid with KnownTrack
	VR       int     // ft/min, valid with KnownVR
	Known    Known
	OnGround bool
}

// MarshalJSON writes {t, lat, lon, alt, altGnss, gs, track, vr, onGround}, omitting unknown values and false flags.
func (p TrackPoint) MarshalJSON() ([]byte, error) {
	w := struct {
		T        int64    `json:"t"`
		Lat      float64  `json:"lat"`
		Lon      float64  `json:"lon"`
		Alt      *int     `json:"alt,omitempty"`
		AltGNSS  bool     `json:"altGnss,omitempty"`
		GS       *float64 `json:"gs,omitempty"`
		Track    *float64 `json:"track,omitempty"`
		VR       *int     `json:"vr,omitempty"`
		OnGround bool     `json:"onGround,omitempty"`
	}{T: p.T, Lat: p.Lat, Lon: p.Lon, AltGNSS: p.AltSrc == AltGNSS, OnGround: p.OnGround}
	if p.AltSrc != AltUnknown {
		w.Alt = new(p.Alt)
	}
	if p.Known&KnownGS != 0 {
		w.GS = new(p.GS)
	}
	if p.Known&KnownTrack != 0 {
		w.Track = new(p.Track)
	}
	if p.Known&KnownVR != 0 {
		w.VR = new(p.VR)
	}
	return json.Marshal(w)
}

// newTrackPoint builds a point at the resolved position pos from the aircraft's current state.
func newTrackPoint(a *Aircraft, pos Position) TrackPoint {
	p := TrackPoint{T: pos.Time.UnixMilli(), Lat: round(pos.Lat, 6), Lon: round(pos.Lon, 6)}
	p.OnGround = a.OnGround != nil && *a.OnGround
	switch {
	case p.OnGround:
	case a.AltBaro != nil:
		p.Alt, p.AltSrc = *a.AltBaro, AltBaro
	case a.AltGNSS != nil:
		p.Alt, p.AltSrc = *a.AltGNSS, AltGNSS
	}
	if a.GroundSpeed != nil {
		p.GS, p.Known = *a.GroundSpeed, p.Known|KnownGS
	}
	if a.Track != nil {
		p.Track, p.Known = *a.Track, p.Known|KnownTrack
	}
	if a.VerticalRate != nil {
		p.VR, p.Known = *a.VerticalRate, p.Known|KnownVR
	}
	return p
}

// keep reports whether p is worth keeping after the previously kept point prev.
func (p TrackPoint) keep(prev TrackPoint, minInterval time.Duration) bool {
	switch {
	case p.T <= prev.T:
		return false
	case p.T-prev.T >= minInterval.Milliseconds(), p.OnGround != prev.OnGround:
		return true
	case p.Known&prev.Known&KnownTrack != 0 && angleDiff(p.Track, prev.Track) >= trailTurnDeg:
		return true
	case p.AltSrc != AltUnknown && prev.AltSrc != AltUnknown && abs(p.Alt-prev.Alt) >= trailAltFt:
		return true
	}
	return false
}

// trimTrail drops the points older than window before the newest one.
func trimTrail(trail []TrackPoint, window time.Duration) []TrackPoint {
	if len(trail) == 0 {
		return trail
	}
	cutoff := trail[len(trail)-1].T - window.Milliseconds()
	i, _ := slices.BinarySearchFunc(trail, cutoff, func(p TrackPoint, t int64) int { return cmp.Compare(p.T, t) })
	return slices.Delete(trail, 0, i)
}
