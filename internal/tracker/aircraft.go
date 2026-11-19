package tracker

import (
	"maps"
	"time"
)

// Aircraft is the public state of one aircraft. Unknown values are nil/empty and omitted from JSON.
// Pointer fields are replaced, never mutated in place, so shallow copies can share them safely.
type Aircraft struct {
	ICAO        string `json:"icao"`
	Callsign    string `json:"callsign,omitempty"`
	Squawk      string `json:"squawk,omitempty"`
	Category    string `json:"category,omitempty"`
	ADSBVersion *int   `json:"adsbVersion,omitempty"`
	Country     string `json:"country,omitempty"`
	Emergency   string `json:"emergency,omitempty"`

	AltBaro      *int     `json:"altBaro,omitempty"`      // ft
	AltGNSS      *int     `json:"altGnss,omitempty"`      // ft
	GroundSpeed  *float64 `json:"groundSpeed,omitempty"`  // kt
	Track        *float64 `json:"track,omitempty"`        // degrees true
	IAS          *int     `json:"ias,omitempty"`          // kt
	TAS          *int     `json:"tas,omitempty"`          // kt
	Heading      *float64 `json:"heading,omitempty"`      // degrees
	VerticalRate *int     `json:"verticalRate,omitempty"` // ft/min
	OnGround     *bool    `json:"onGround,omitempty"`

	Lat        *float64   `json:"lat,omitempty"`
	Lon        *float64   `json:"lon,omitempty"`
	PosTime    *time.Time `json:"posTime,omitempty"`
	DistanceNm *float64   `json:"distanceNm,omitempty"`
	Bearing    *float64   `json:"bearing,omitempty"`

	FirstSeen    time.Time      `json:"firstSeen"`
	LastSeen     time.Time      `json:"lastSeen"`
	Messages     uint64         `json:"messages"`
	MessagesByDF map[int]uint64 `json:"messagesByDf"`
	RSSI         *float64       `json:"rssi,omitempty"` // dBFS, Beast input only

	HasPosition bool `json:"hasPosition"`
	ADSB        bool `json:"adsb"` // sent at least one DF17/18
}

func (a *Aircraft) clone() Aircraft {
	c := *a
	c.MessagesByDF = maps.Clone(a.MessagesByDF)
	return c
}
