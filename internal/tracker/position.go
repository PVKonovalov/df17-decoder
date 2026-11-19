package tracker

import (
	"errors"
	"time"

	"github.com/PVKonovalov/df17-decoder/internal/geo"
	"github.com/PVKonovalov/df17-decoder/internal/modes"
)

const (
	// maxPlausibleSpeedKt bounds the implied speed between consecutive positions.
	maxPlausibleSpeedKt = 1500
	// receiverLocalRangeNm is how far from the receiver a receiver-referenced local CPR decode is unambiguous.
	receiverLocalRangeNm = 180
)

var (
	ErrNoReference     = errors.New("position: waiting for an even/odd pair")
	ErrOutOfRange      = errors.New("position: farther than receiver.maxRangeNm")
	ErrImpossibleSpeed = errors.New("position: implies an impossible speed")
)

type Method int

const (
	MethodGlobal        Method = iota + 1 // even/odd pair
	MethodLocalAircraft                   // single frame, reference = aircraft's last position
	MethodLocalReceiver                   // single frame, reference = receiver
)

func (m Method) String() string {
	switch m {
	case MethodGlobal:
		return "global"
	case MethodLocalAircraft:
		return "local-aircraft"
	case MethodLocalReceiver:
		return "local-receiver"
	}
	return "unknown"
}

type Position struct {
	geo.Point
	Time   time.Time
	Method Method
}

type cprSample struct {
	frame modes.CPRFrame
	time  time.Time
}

// CPRState is the per-aircraft memory needed to decode CPR positions. The zero value is ready to use.
type CPRState struct {
	even, odd *cprSample
	last      *Position
}

// Last returns the last accepted position, or nil.
func (s *CPRState) Last() *Position { return s.last }

// PositionResolver turns CPR frames into checked positions.
type PositionResolver struct {
	Receiver   geo.Point
	MaxRangeNm float64
	PairMaxAge time.Duration // max time between the even and odd frame of a global pair
	RefMaxAge  time.Duration // max age of the aircraft's last position used as a local reference / speed check
}

// Resolve stores frame f received at t in st and returns a position for it.
// Order: global decode if a fresh pair exists; otherwise local decode against the aircraft's recent position;
// otherwise local decode against the receiver (accepted only within 180 NM). Results failing the range or speed
// check are rejected; a rejected global decode also drops the aircraft's last position so a bad fix cannot
// lock out good ones.
// The 1090 MHz Riddle, "Airborne position" (https://mode-s.org/decode/content/ads-b/3-airborne-position.html).
func (r PositionResolver) Resolve(st *CPRState, f modes.CPRFrame, t time.Time) (Position, error) {
	sample := &cprSample{frame: f, time: t}
	other := st.even
	if f.Odd {
		st.odd = sample
	} else {
		st.even, other = sample, st.odd
	}

	globalErr := ErrNoReference
	if other != nil && absDuration(t.Sub(other.time)) <= r.PairMaxAge {
		even, odd := st.even.frame, st.odd.frame
		lat, lon, err := modes.CPRGlobal(even, odd, f.Odd)
		if err == nil {
			pos := Position{Point: geo.Point{Lat: lat, Lon: lon}, Time: t, Method: MethodGlobal}
			if err = r.check(st, pos); err != nil {
				if errors.Is(err, ErrImpossibleSpeed) {
					st.last = nil
				}
				return pos, err
			}
			st.last = &pos
			return pos, nil
		}
		globalErr = err
	}

	pos := Position{Time: t, Method: MethodLocalReceiver}
	ref := r.Receiver
	if st.last != nil && t.Sub(st.last.Time) <= r.RefMaxAge {
		pos.Method, ref = MethodLocalAircraft, st.last.Point
	}
	pos.Lat, pos.Lon = modes.CPRLocal(f, ref.Lat, ref.Lon)

	if pos.Method == MethodLocalReceiver && geo.DistanceNm(r.Receiver, pos.Point) > receiverLocalRangeNm {
		return pos, errors.Join(globalErr, ErrOutOfRange)
	}
	if err := r.check(st, pos); err != nil {
		return pos, err
	}
	st.last = &pos
	return pos, nil
}

func (r PositionResolver) check(st *CPRState, pos Position) error {
	if geo.DistanceNm(r.Receiver, pos.Point) > r.MaxRangeNm {
		return ErrOutOfRange
	}
	if prev := st.last; prev != nil {
		dt := pos.Time.Sub(prev.Time)
		if dt >= 0 && dt <= r.RefMaxAge {
			allowedNm := (dt + time.Second).Hours() * maxPlausibleSpeedKt
			if geo.DistanceNm(prev.Point, pos.Point) > allowedNm {
				return ErrImpossibleSpeed
			}
		}
	}
	return nil
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
