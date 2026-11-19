package tracker

import "time"

// KnownAddresses remembers ICAO addresses seen in frames with a real CRC check (DF11/17/18), so that
// address/parity replies (whose address is only the CRC remainder) can be accepted for them and dropped
// otherwise. Not safe for concurrent use; it belongs to the decoding goroutine.
type KnownAddresses struct {
	ttl       time.Duration
	seen      map[uint32]time.Time
	lastSweep time.Time
}

// NewKnownAddresses returns a table whose entries expire ttl after they were last seen.
func NewKnownAddresses(ttl time.Duration) *KnownAddresses {
	return &KnownAddresses{ttl: ttl, seen: map[uint32]time.Time{}}
}

// Add marks icao as seen at t and sweeps expired entries at most once per ttl.
func (k *KnownAddresses) Add(icao uint32, t time.Time) {
	if t.After(k.seen[icao]) {
		k.seen[icao] = t
	}
	if t.Sub(k.lastSweep) >= k.ttl {
		k.Expire(t)
	}
}

// Known reports whether icao was seen within ttl before t.
func (k *KnownAddresses) Known(icao uint32, t time.Time) bool {
	last, ok := k.seen[icao]
	return ok && t.Sub(last) <= k.ttl
}

// Expire removes entries older than ttl at now and returns how many were removed.
func (k *KnownAddresses) Expire(now time.Time) int {
	n := 0
	for icao, last := range k.seen {
		if now.Sub(last) > k.ttl {
			delete(k.seen, icao)
			n++
		}
	}
	k.lastSweep = now
	return n
}

// Len returns the number of addresses currently stored (including not yet swept expired ones).
func (k *KnownAddresses) Len() int { return len(k.seen) }
