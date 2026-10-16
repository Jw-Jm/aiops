package analytics

import (
	"slices"
	"time"
)

// SourceRequestCost returns how many backend source requests one analysis query
// expands to, so the investigation budget can atomically reserve the real number
// before any remote request is issued.
//
// Semantics are derived from the frozen intent and the analysis catalog, not
// from the current backend state:
//   - graph and postgresql datasets execute a single statement.
//   - victoriametrics trend is one range query with a step; compare/compare_percent
//     evaluate two windows; aggregate/average is one instant query.
//   - victorialogs trend issues one bounded request per inclusive grid point;
//     compare/compare_percent evaluate two windows; aggregate/samples is one request.
//   - deepflow (network) trend evaluates half-open buckets up to, excluding, the
//     window end; compare/compare_percent evaluate two windows.
//
// Operations whose expansion cannot be bounded from the intent alone are
// rejected instead of being under-reserved.
func SourceRequestCost(intent QueryIntent) (int64, error) {
	if intent.TimeRange == nil || intent.TimeRange.From.IsZero() || !intent.TimeRange.To.After(intent.TimeRange.From) {
		return 0, ErrInvalid
	}
	catalog, err := Catalog()
	if err != nil {
		return 0, err
	}
	index := slices.IndexFunc(catalog.Datasets, func(d Dataset) bool { return d.ID == intent.DatasetID })
	if index < 0 {
		return 0, ErrUnsupported
	}
	dataset := catalog.Datasets[index]
	if !slices.Contains(dataset.Operations, intent.Operation) {
		return 0, ErrUnsupported
	}
	switch intent.Operation {
	case "compare", "compare_percent":
		if intent.ComparisonTimeRange == nil {
			return 0, ErrInvalid
		}
		return 2, nil
	case "trend":
		if intent.StepSeconds < 60 {
			return 0, ErrInvalid
		}
		seconds := int64(intent.TimeRange.To.Sub(intent.TimeRange.From) / time.Second)
		if seconds <= 0 || seconds%int64(intent.StepSeconds) != 0 {
			return 0, ErrInvalid
		}
		points := seconds / int64(intent.StepSeconds)
		switch dataset.Source {
		case "deepflow":
			// Half-open buckets: the observation ending at the window end is excluded.
		case "victorialogs":
			// One bounded request per inclusive grid point, including the end.
			points++
		case "victoriametrics":
			// A single range query carries the whole window with its step.
			points = 1
		default:
			return 0, ErrUnsupported
		}
		if points < 1 {
			return 0, ErrInvalid
		}
		return points, nil
	case "aggregate", "average", "samples":
		switch dataset.Source {
		case "graph", "postgresql", "victoriametrics", "victorialogs", "deepflow":
			return 1, nil
		default:
			return 0, ErrUnsupported
		}
	default:
		return 0, ErrUnsupported
	}
}
