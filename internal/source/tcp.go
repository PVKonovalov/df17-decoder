package source

import (
	"context"
	"net"
	"time"
)

// TCP reads AVR lines from a dump1090 raw output port and reconnects with exponential backoff.
type TCP struct {
	Address string
	Opt     Options
	stats   Stats
}

func (t *TCP) Stats() *Stats { return &t.stats }

// Run returns only when ctx is cancelled.
func (t *TCP) Run(ctx context.Context, out chan<- Frame) error {
	log := t.Opt.Logger.With("source", "tcp://"+t.Address)
	backoff := t.Opt.ReconnectMin
	var dialer net.Dialer

	for {
		conn, err := dialer.DialContext(ctx, "tcp", t.Address)
		if err == nil {
			log.Info("connected")
			backoff = t.Opt.ReconnectMin
			t.stats.connected.Store(true)
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			err = readAVR(ctx, conn, out, &t.stats, nil)
			stop()
			conn.Close()
			t.stats.connected.Store(false)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Warn("disconnected, will retry", "err", err, "in", backoff)

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return ctx.Err()
		}
		t.stats.reconnects.Add(1)
		backoff = min(backoff*2, t.Opt.ReconnectMax)
	}
}
