package analytics

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
)

// Range timestamps must lie on the compiled grid. No model interpolation,
// duplicate points, non-finite values or absent observations become facts.
func trendPoint(point []json.RawMessage, window *QueryTimeRange, step int) (Row, error) {
	if len(point) != 2 {
		return Row{}, ErrSource
	}
	var timestamp float64
	var valueText string
	if json.Unmarshal(point[0], &timestamp) != nil || json.Unmarshal(point[1], &valueText) != nil || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) {
		return Row{}, ErrSource
	}
	offset := timestamp - float64(window.From.Unix())
	if offset < 0 || timestamp > float64(window.To.Unix()) || math.Abs(offset-math.Round(offset/float64(step))*float64(step)) > 0.000001 {
		return Row{}, ErrSource
	}
	value, err := strconv.ParseFloat(valueText, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1<<53 {
		return Row{}, ErrSource
	}
	return Row{Dimension: time.Unix(int64(math.Round(timestamp)), 0).UTC().Format(time.RFC3339), Value: value}, nil
}
