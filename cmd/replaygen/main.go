// Command replaygen writes a synthetic AVR capture with many moving aircraft around a receiver, for
// exercising the web UI and the decoder without a radio:
//
//	go run ./cmd/replaygen -o testdata/busy.txt
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"

	"github.com/PVKonovalov/df17-decoder/internal/config"
)

const clockHz = 12_000_000

type options struct {
	lat, lon     float64 // receiver
	count        int     // aircraft
	modeSOnly    float64 // fraction without ADS-B
	duration     float64 // seconds
	step         float64 // seconds between simulation ticks
	maxRangeNm   float64
	seed         uint64
	emergencySqk string
	arrivals     float64 // fraction of aircraft that appear during the capture rather than at its start
	departures   float64 // fraction of aircraft that stop transmitting before the end
}

type aircraft struct {
	icao       uint32
	callsign   string
	squawk     string
	adsb       bool
	lat, lon   float64
	alt        float64 // ft
	gs         float64 // kt
	track      float64 // degrees
	vr         float64 // ft/min
	turn       float64 // degrees/s
	emergency  bool
	odd        bool
	phase      int // spreads periodic messages over ticks
	identEvery int // ticks
	start, end int // transmits on ticks [start, end)
}

var airlines = []string{"ELY", "ISR", "AIZ", "WZZ", "RYR", "UAE", "THY", "DLH", "AFR", "BAW", "KLM", "AEE"}

// icaoBlocks are address prefixes (Israel and common European blocks) used for random addresses.
var icaoBlocks = []uint32{0x738000, 0x4CA000, 0x3C6000, 0x484000, 0x400000, 0x468000, 0x4B8000, 0x380000}

func newFleet(o options, r *rand.Rand) []*aircraft {
	fleet := make([]*aircraft, o.count)
	seen := map[uint32]bool{}
	ticks := int(o.duration / o.step)
	minLife := int(120 / o.step)
	for i := range fleet {
		var icao uint32
		for icao == 0 || seen[icao] {
			icao = icaoBlocks[r.IntN(len(icaoBlocks))] | uint32(r.IntN(0x1000))
		}
		seen[icao] = true

		distNm := o.maxRangeNm * math.Sqrt(r.Float64()) * 0.8
		brg := r.Float64() * 2 * math.Pi
		a := &aircraft{
			icao:       icao,
			adsb:       r.Float64() >= o.modeSOnly,
			lat:        o.lat + distNm*math.Cos(brg)/60,
			lon:        o.lon + distNm*math.Sin(brg)/(60*math.Cos(o.lat*math.Pi/180)),
			track:      r.Float64() * 360,
			turn:       (r.Float64() - 0.5) * 0.6,
			squawk:     fmt.Sprintf("%o", 0o1000+r.IntN(0o6000)),
			phase:      r.IntN(10),
			identEvery: 10,
			end:        ticks,
		}
		arriving := ticks > 1 && r.Float64() < o.arrivals
		if arriving {
			a.start = 1 + r.IntN(max(1, ticks*9/10))
		}
		if r.Float64() < o.departures && a.start+minLife < ticks {
			a.end = a.start + minLife + r.IntN(ticks-a.start-minLife)
		}
		if a.adsb {
			a.callsign = fmt.Sprintf("%s%d", airlines[r.IntN(len(airlines))], 10+r.IntN(990))
			a.alt = 2000 + r.Float64()*37000
			a.gs = 220 + r.Float64()*260
			a.vr = []float64{0, 0, 0, 1500, -1200, 2200, -800}[r.IntN(7)]
			if arriving { // enters coverage at the edge, heading roughly towards the receiver
				edge := o.maxRangeNm * 0.85
				a.lat = o.lat + edge*math.Cos(brg)/60
				a.lon = o.lon + edge*math.Sin(brg)/(60*math.Cos(o.lat*math.Pi/180))
				a.track = math.Mod(brg*180/math.Pi+180+(r.Float64()-0.5)*80+360, 360)
			}
		} else { // light GA without ADS-B
			a.alt = 1000 + r.Float64()*7000
			a.gs = 90 + r.Float64()*60
			a.vr = []float64{0, 0, 500, -500}[r.IntN(4)]
		}
		fleet[i] = a
	}
	if len(fleet) > 0 && o.emergencySqk != "" {
		e := fleet[0]
		e.adsb, e.emergency, e.squawk, e.callsign = true, true, o.emergencySqk, "ELY7700"
		e.start, e.end = 0, ticks
	}
	return fleet
}

// move advances the aircraft by dt seconds, turning back towards the receiver near the edge of range and
// reversing climbs and descents at the altitude limits.
func (a *aircraft) move(o options, dt float64) {
	dLat := (a.lat - o.lat) * 60
	dLon := (a.lon - o.lon) * 60 * math.Cos(o.lat*math.Pi/180)
	if math.Hypot(dLat, dLon) > o.maxRangeNm*0.9 {
		home := math.Mod(math.Atan2(-dLon, -dLat)*180/math.Pi+360, 360)
		diff := math.Mod(home-a.track+540, 360) - 180
		a.track += math.Copysign(math.Min(math.Abs(diff), 3*dt), diff)
	} else {
		a.track += a.turn * dt
	}
	a.track = math.Mod(a.track+360, 360)

	rad := a.track * math.Pi / 180
	nm := a.gs * dt / 3600
	a.lat += nm * math.Cos(rad) / 60
	a.lon += nm * math.Sin(rad) / (60 * math.Cos(a.lat*math.Pi/180))
	a.alt += a.vr * dt / 60
	if (a.alt > 41000 && a.vr > 0) || (a.alt < 800 && a.vr < 0) {
		a.vr = -a.vr
	}
}

// frames returns the frames the aircraft transmits on tick n.
func (a *aircraft) frames(n int) [][]byte {
	if n < a.start || n >= a.end {
		return nil
	}
	var out [][]byte
	if a.adsb {
		out = append(out, airbornePosition(a.icao, a.lat, a.lon, a.alt, a.odd))
		a.odd = !a.odd
		if (n+a.phase)%2 == 0 {
			out = append(out, velocity(a.icao, a.gs, a.track, a.vr))
		}
		if (n+a.phase)%a.identEvery == 0 {
			out = append(out, identification(a.icao, a.callsign))
			if a.emergency {
				out = append(out, emergencyStatus(a.icao, 1, a.squawk))
			}
		}
		return out
	}
	switch (n + a.phase) % 4 {
	case 0:
		out = append(out, allCall(a.icao))
	case 1, 3:
		out = append(out, altitudeReply(a.icao, a.alt))
	case 2:
		out = append(out, identityReply(a.icao, a.squawk))
	}
	return out
}

// generate writes @-timestamped AVR lines for the whole simulation to w and returns the number of lines.
func generate(w io.Writer, o options) (int, error) {
	r := rand.New(rand.NewPCG(o.seed, o.seed^0x9E3779B97F4A7C15))
	fleet := newFleet(o, r)
	bw := bufio.NewWriter(w)
	lines := 0
	ticks := int(o.duration / o.step)
	tickClk := uint64(o.step * clockHz)
	for n := range ticks {
		var ts uint64 = clockHz + uint64(n)*tickClk // start at 1 s: 0 means "no timestamp"
		for _, a := range fleet {
			for _, f := range a.frames(n) {
				ts += 1200 // 100 µs between frames keeps timestamps strictly increasing within a tick
				if _, err := fmt.Fprintf(bw, "@%012X%X;\n", ts, f); err != nil {
					return lines, err
				}
				lines++
			}
			a.move(o, o.step)
		}
	}
	return lines, bw.Flush()
}

// applyConfig takes the receiver position from the service config at path, unless -lat/-lon were given
// (explicit holds the flag names set on the command line), and keeps the traffic inside receiver.maxRangeNm.
func applyConfig(o *options, path string, explicit map[string]bool) error {
	cfg, err := config.Load(path, config.Overrides{})
	if err != nil {
		return err
	}
	if !explicit["lat"] {
		o.lat = cfg.Receiver.Lat
	}
	if !explicit["lon"] {
		o.lon = cfg.Receiver.Lon
	}
	if !explicit["range"] {
		o.maxRangeNm = min(o.maxRangeNm, cfg.Receiver.MaxRangeNm*0.9)
	}
	return nil
}

func main() {
	var o options
	out := flag.String("o", "testdata/busy.txt", "output file (- for stdout)")
	cfgPath := flag.String("config", "", "service config to take the receiver position from (-lat/-lon override it)")
	flag.Float64Var(&o.lat, "lat", 25.07, "receiver latitude")
	flag.Float64Var(&o.lon, "lon", 22.87, "receiver longitude")
	flag.IntVar(&o.count, "n", 150, "number of aircraft")
	flag.Float64Var(&o.modeSOnly, "modes-only", 0.15, "fraction of aircraft without ADS-B (DF4/5/11 only)")
	flag.Float64Var(&o.duration, "duration", 600, "capture length, seconds")
	flag.Float64Var(&o.maxRangeNm, "range", 150, "radius the traffic stays within, nm")
	flag.Uint64Var(&o.seed, "seed", 1, "random seed")
	flag.StringVar(&o.emergencySqk, "emergency", "7700", "squawk of one emergency aircraft (empty: none)")
	flag.Float64Var(&o.arrivals, "arrivals", 0.4, "fraction of aircraft that appear during the capture")
	flag.Float64Var(&o.departures, "departures", 0.3, "fraction of aircraft that stop transmitting before the end")
	flag.Parse()
	o.step = 0.5
	if *cfgPath != "" {
		explicit := map[string]bool{}
		flag.Visit(func(f *flag.Flag) { explicit[f.Name] = true })
		if err := applyConfig(&o, *cfgPath, explicit); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	w := os.Stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	n, err := generate(w, o)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %d frames for %d aircraft over %.0f s around %.4f, %.4f\n", n, o.count, o.duration, o.lat, o.lon)
}
