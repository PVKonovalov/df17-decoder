package stats

import (
	"maps"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

const rateWindow = 60 // seconds

// Stats are service-wide counters, written by the decoding goroutine and read by the API.
type Stats struct {
	start time.Time

	Frames             atomic.Uint64
	Messages           atomic.Uint64 // decoded and accepted by the tracker
	CRCFailures        atomic.Uint64
	FixedBitErrors     atomic.Uint64
	Unsupported        atomic.Uint64
	Invalid            atomic.Uint64
	AddressParityDrops atomic.Uint64
	Positions          atomic.Uint64
	CPRRejected        atomic.Uint64
	NoPosition         atomic.Uint64 // ADS-B TC 0: the transponder reports no valid position
	CommBAmbiguous     atomic.Uint64 // DF20/21 MB fields that fit more than one register
	CommBRejected      atomic.Uint64 // inferred registers inconsistent with the aircraft's recent state
	SignalFrames       atomic.Uint64 // frames with a signal level (Beast)
	signalPower        atomic.Uint64 // their summed linear power, × signalScale

	mu      sync.Mutex
	perDF   map[int]uint64
	buckets [rateWindow]uint64
	seconds [rateWindow]int64
}

func New(start time.Time) *Stats {
	return &Stats{start: start, perDF: map[int]uint64{}}
}

// AddFrame counts one received frame of downlink format df.
func (s *Stats) AddFrame(df int) {
	s.Frames.Add(1)
	s.mu.Lock()
	s.perDF[df]++
	s.mu.Unlock()
}

const signalScale = 1e9

// AddSignal adds one frame's signal level in dBFS to the hourly average signal.
func (s *Stats) AddSignal(dbfs float64) {
	s.SignalFrames.Add(1)
	s.signalPower.Add(uint64(math.Round(math.Pow(10, dbfs/10) * signalScale)))
}

// SignalPower returns the summed linear power of the frames counted in SignalFrames.
func (s *Stats) SignalPower() float64 { return float64(s.signalPower.Load()) / signalScale }

// AddMessage counts one accepted message at wall time now for the messages-per-second rate.
func (s *Stats) AddMessage(now time.Time) {
	s.Messages.Add(1)
	sec := now.Unix()
	i := sec % rateWindow
	s.mu.Lock()
	if s.seconds[i] != sec {
		s.seconds[i], s.buckets[i] = sec, 0
	}
	s.buckets[i]++
	s.mu.Unlock()
}

type Snapshot struct {
	Uptime             time.Duration
	Frames             uint64
	FramesPerDF        map[int]uint64
	Messages           uint64
	MessagesPerSecond  float64 // average over the last 60 s
	CRCFailures        uint64
	FixedBitErrors     uint64
	Unsupported        uint64
	Invalid            uint64
	AddressParityDrops uint64
	Positions          uint64
	CPRRejected        uint64
	NoPosition         uint64
	CommBAmbiguous     uint64
	CommBRejected      uint64
}

// Snapshot returns a consistent-enough copy of all counters at wall time now.
func (s *Stats) Snapshot(now time.Time) Snapshot {
	snap := Snapshot{
		Uptime:             now.Sub(s.start),
		Frames:             s.Frames.Load(),
		Messages:           s.Messages.Load(),
		CRCFailures:        s.CRCFailures.Load(),
		FixedBitErrors:     s.FixedBitErrors.Load(),
		Unsupported:        s.Unsupported.Load(),
		Invalid:            s.Invalid.Load(),
		AddressParityDrops: s.AddressParityDrops.Load(),
		Positions:          s.Positions.Load(),
		CPRRejected:        s.CPRRejected.Load(),
		NoPosition:         s.NoPosition.Load(),
		CommBAmbiguous:     s.CommBAmbiguous.Load(),
		CommBRejected:      s.CommBRejected.Load(),
	}
	cur := now.Unix()
	var sum uint64
	s.mu.Lock()
	snap.FramesPerDF = maps.Clone(s.perDF)
	for i, sec := range s.seconds {
		if sec > cur-rateWindow && sec <= cur {
			sum += s.buckets[i]
		}
	}
	s.mu.Unlock()
	snap.MessagesPerSecond = float64(sum) / rateWindow
	return snap
}
