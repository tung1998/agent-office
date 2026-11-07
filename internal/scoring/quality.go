package scoring

import (
	"math"

	"github.com/tung1998/agent-office/internal/storage"
)

// Points of Q's criteria (spec: merged 40, no rework 20, no correction 20,
// checks green 20), normalised over the ones that apply.
const (
	PointsMerged       = 40
	PointsNoRework     = 20
	PointsNoCorrection = 20
	PointsChecksGreen  = 20
	// Threshold is the Q a configuration needs to count as good enough.
	Threshold = 80
)

// Quality is a job's Q (0..100), nil when no criterion applies.
func Quality(p storage.ScoreParts) *int {
	var got, total int
	add := func(b *bool, pts int) {
		if b == nil {
			return
		}
		total += pts
		if *b {
			got += pts
		}
	}
	add(p.Merged, PointsMerged)
	add(p.NoRework, PointsNoRework)
	add(p.NoCorrection, PointsNoCorrection)
	add(p.ChecksGreen, PointsChecksGreen)
	if total == 0 {
		return nil
	}
	q := int(math.Round(float64(got) * 100 / float64(total)))
	return &q
}
