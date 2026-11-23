package tracker

import "time"

// Quality is an ADS-B aircraft's own report of its position: QualityOK, QualityDegraded or QualityLost.
type Quality string

const (
	QualityOK       Quality = "ok"
	QualityDegraded Quality = "degraded" // integrity or accuracy below the ADS-B Out mandate: NIC < 7 or NACp < 8
	QualityLost     Quality = "lost"     // TC 0 (no position) since the last position, or no position for positionTimeout

	minNIC  = 7 // containment radius < 0.2 NM
	minNACp = 8 // estimated position uncertainty < 93 m

	// positionTimeout is how long an aircraft may send other ADS-B messages without a position before its position
	// counts as lost; airborne positions come twice a second, stationary surface ones every 5 s.
	positionTimeout = 30 * time.Second
)

// qualityOf returns the position quality at data time at from the last NIC and NACp (nil when unknown), the time
// of the last position message and of the last TC 0 (zero when none); "" while nothing is known.
func qualityOf(nic, nacp *int, posTime, noPosTime, at time.Time) Quality {
	switch {
	case !noPosTime.IsZero() && noPosTime.After(posTime):
		return QualityLost
	case !posTime.IsZero() && at.Sub(posTime) > positionTimeout:
		return QualityLost
	case nic != nil && *nic < minNIC, nacp != nil && *nacp < minNACp:
		return QualityDegraded
	case nic != nil, nacp != nil:
		return QualityOK
	}
	return ""
}

// QualityChange is a flight's position quality from data time T (unix ms) on.
type QualityChange struct {
	T       int64
	Quality Quality
}
