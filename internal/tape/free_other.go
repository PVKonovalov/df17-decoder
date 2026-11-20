//go:build !unix

package tape

import "math"

// freeBytes is not implemented here; the disk guard never triggers.
func freeBytes(string) (uint64, error) { return math.MaxUint64, nil }
