package tracker

import (
	"testing"
	"time"
)

func TestKnownAddresses(t *testing.T) {
	k := NewKnownAddresses(60 * time.Second)

	if k.Known(0x738062, t0) {
		t.Fatal("unknown address reported as known")
	}
	k.Add(0x738062, t0)
	if !k.Known(0x738062, t0.Add(60*time.Second)) {
		t.Error("address must be known up to ttl")
	}
	if k.Known(0x738062, t0.Add(61*time.Second)) {
		t.Error("address must expire after ttl")
	}

	k.Add(0x738062, t0.Add(50*time.Second)) // refresh
	if !k.Known(0x738062, t0.Add(100*time.Second)) {
		t.Error("refresh must extend the lifetime")
	}

	k.Add(0x738062, t0.Add(10*time.Second)) // out-of-order older frame must not shorten it
	if !k.Known(0x738062, t0.Add(100*time.Second)) {
		t.Error("older timestamp shortened the lifetime")
	}
}

func TestKnownAddresses_Sweep(t *testing.T) {
	k := NewKnownAddresses(10 * time.Second)
	k.Add(1, t0)
	k.Add(2, t0.Add(5*time.Second))
	if n := k.Expire(t0.Add(12 * time.Second)); n != 1 || k.Len() != 1 {
		t.Errorf("expired %d, len %d; want 1, 1", n, k.Len())
	}

	// Add sweeps automatically once per ttl
	k.Add(3, t0.Add(30*time.Second))
	if k.Len() != 1 || !k.Known(3, t0.Add(30*time.Second)) {
		t.Errorf("len = %d after auto-sweep, want 1", k.Len())
	}
}
