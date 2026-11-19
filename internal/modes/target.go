package modes

// TargetState is the content of TC 29 subtype 1 (ADS-B version 2). Unavailable values are nil.
type TargetState struct {
	SelectedAltitude    *int     // ft, MCP/FCU or FMS selected altitude
	SelectedAltitudeFMS bool     // SelectedAltitude source: true FMS, false MCP/FCU
	BaroSetting         *float64 // hPa (millibar)
	SelectedHeading     *float64 // degrees
	Modes               *AutopilotModes
}

// AutopilotModes are the MCP/FCU mode bits, present only when their status bit is set.
type AutopilotModes struct {
	Autopilot, VNAV, AltitudeHold, Approach, TCAS, LNAV bool
}

// Names lists the engaged modes, e.g. ["autopilot", "vnav", "lnav", "tcas"].
func (m AutopilotModes) Names() []string {
	names := []string{}
	for _, f := range []struct {
		on   bool
		name string
	}{{m.Autopilot, "autopilot"}, {m.VNAV, "vnav"}, {m.AltitudeHold, "althold"}, {m.Approach, "approach"}, {m.LNAV, "lnav"}, {m.TCAS, "tcas"}} {
		if f.on {
			names = append(names, f.name)
		}
	}
	return names
}

// decodeTargetState decodes TC 29 subtype 1. ME layout: ST 6–7, SIL 8, ALT source 9, ALT 10–20, BARO 21–29,
// HDG status 30, HDG 31–39, NACp 40–43, NICbaro 44, SIL 45–46, mode status 47, AP 48, VNAV 49, ALT hold 50,
// approach 52, TCAS 53, LNAV 54. Subtype 0 (version 1) has another layout and returns ErrUnsupportedTC.
// The 1090 MHz Riddle, "Aircraft status" → "Target state and status" (https://mode-s.org/1090mhz/content/ads-b/6-other-messages.html).
func decodeTargetState(msg []byte) (TargetState, error) {
	if bits(msg, 38, 2) != 1 {
		return TargetState{}, ErrUnsupportedTC
	}
	ts := TargetState{SelectedAltitudeFMS: bits(msg, 41, 1) == 1}
	if alt := int(bits(msg, 42, 11)); alt != 0 {
		ts.SelectedAltitude = new((alt - 1) * 32)
	}
	if baro := bits(msg, 53, 9); baro != 0 {
		ts.BaroSetting = new(800 + float64(baro-1)*0.8)
	}
	if bits(msg, 62, 1) == 1 {
		ts.SelectedHeading = new(float64(bits(msg, 63, 9)) * 180 / 256)
	}
	if bits(msg, 79, 1) == 1 {
		ts.Modes = &AutopilotModes{
			Autopilot:    bits(msg, 80, 1) == 1,
			VNAV:         bits(msg, 81, 1) == 1,
			AltitudeHold: bits(msg, 82, 1) == 1,
			Approach:     bits(msg, 84, 1) == 1,
			TCAS:         bits(msg, 85, 1) == 1,
			LNAV:         bits(msg, 86, 1) == 1,
		}
	}
	return ts, nil
}
