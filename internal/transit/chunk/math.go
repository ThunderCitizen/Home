package chunk

import "math"

// Cv is the population standard deviation divided by the mean headway.
// Call only within a homogeneous stop/direction/day/band window. Summing
// different timetables before taking variance invents irregularity.
func Cv(n int, sum, sumSq float64) float64 {
	if n < 2 || sum <= 0 {
		return 0
	}
	mean := sum / float64(n)
	return math.Sqrt(math.Max(0, sumSq/float64(n)-mean*mean)) / mean
}
