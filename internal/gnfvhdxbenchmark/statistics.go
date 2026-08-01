package gnfvhdxbenchmark

import (
	"fmt"
	"math"
	"sort"
)

type Statistics struct {
	Count             int     `json:"count"`
	Mean              float64 `json:"mean"`
	Median            float64 `json:"median"`
	Min               float64 `json:"min"`
	Max               float64 `json:"max"`
	P95               float64 `json:"p95"`
	StandardDeviation float64 `json:"standardDeviation"`
}

func CalculateStatistics(values []float64) (Statistics, error) {
	if len(values) == 0 {
		return Statistics{}, fmt.Errorf("at least one value is required")
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	var sum float64
	for _, value := range sorted {
		sum += value
	}
	mean := sum / float64(len(sorted))
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	var squared float64
	for _, value := range sorted {
		delta := value - mean
		squared += delta * delta
	}
	p95Index := int(math.Ceil(0.95*float64(len(sorted)))) - 1
	return Statistics{Count: len(sorted), Mean: mean, Median: median, Min: sorted[0], Max: sorted[len(sorted)-1], P95: sorted[p95Index], StandardDeviation: math.Sqrt(squared / float64(len(sorted)))}, nil
}

func PercentChange(reference, candidate float64) *float64 {
	if reference == 0 {
		return nil
	}
	value := (candidate - reference) / reference * 100
	return &value
}
