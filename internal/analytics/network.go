package analytics

import (
	"context"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"ops-platform/internal/evidence"
	"ops-platform/internal/graph"
	"ops-platform/internal/integrations/deepflow"
	"ops-platform/internal/resource"
)

type Network struct {
	adapter        *deepflow.Adapter
	qualifications []Qualification
}
type NetworkPlan struct {
	intent                QueryIntent
	scope                 graph.Scope
	proof                 Qualification
	dataset               Dataset
	digest, catalogDigest string
}

func NewNetwork(adapter *deepflow.Adapter, proofs []Qualification) *Network {
	return &Network{adapter, slices.Clone(proofs)}
}
func (p NetworkPlan) ExecutionScope() graph.Scope { return CloneScope(p.scope) }
func (p NetworkPlan) Digest() string              { return p.digest }

func (n *Network) Compile(input QueryIntent, binding Binding) (NetworkPlan, error) {
	var p NetworkPlan
	intent, err := DecodeIntent(rawIntent(input))
	if err != nil {
		return p, err
	}
	if n == nil || n.adapter == nil || intent.NetworkSelection == nil {
		return p, ErrUnsupported
	}
	catalog, err := Catalog()
	if err != nil {
		return p, err
	}
	for _, d := range catalog.Datasets {
		if d.ID == intent.DatasetID {
			p.dataset = d
		}
	}
	if p.dataset.Source != "deepflow" || p.dataset.AuthorizationDomain != "operational" || !slices.Contains(p.dataset.Operations, intent.Operation) || len(intent.Metrics) != 1 || intent.Metrics[0] != p.dataset.Metric || len(intent.Filters) > 0 || len(intent.Sort) > 0 || intent.Limit < 1 || intent.Limit > 50 {
		return p, ErrUnsupported
	}
	scope, err := RestrictScope(binding.Grant, binding.Selection)
	if err != nil {
		return p, err
	}
	b := n.adapter.Binding()
	if scope.Tenant != b.Tenant || !slices.Contains(b.ScopeMapping.Scopes["cluster"], scope.Cluster) {
		return p, ErrDenied
	}
	s := intent.NetworkSelection
	if !slices.Contains([]string{"incoming", "outgoing", "either"}, s.Direction) {
		return p, ErrInvalid
	}
	for _, id := range []string{s.TargetCanonicalID, s.PeerCanonicalID} {
		v, e := resource.ParseCanonicalID(id)
		if e != nil || v.Tenant != scope.Tenant || v.Scope != scope.Cluster || v.Domain != "k8s" || v.Kind != "Pod" || len(scope.Resources) > 0 && !slices.Contains(scope.Resources, id) {
			return p, ErrDenied
		}
	}
	window := intent.TimeRange
	if window == nil || window.From.IsZero() || !window.To.After(window.From) || window.To.Sub(window.From) > time.Hour || window.To.After(time.Now()) || window.From.Nanosecond() != 0 || window.To.Nanosecond() != 0 || window.Timezone == "Local" {
		return p, ErrInvalid
	}
	if _, err = time.LoadLocation(window.Timezone); err != nil {
		return p, ErrInvalid
	}
	compare := intent.Operation == "compare" || intent.Operation == "compare_percent"
	if compare {
		w := intent.ComparisonTimeRange
		if w == nil || w.From.IsZero() || !w.To.After(w.From) || w.To.After(window.From) || w.To.Sub(w.From) != window.To.Sub(window.From) || w.Timezone != window.Timezone || w.From.Nanosecond() != 0 || w.To.Nanosecond() != 0 || len(intent.Dimensions) != 1 || intent.Dimensions[0] != "comparison" || intent.Limit < 3 {
			return p, ErrInvalid
		}
	} else if intent.ComparisonTimeRange != nil {
		return p, ErrInvalid
	}
	if intent.Operation == "trend" {
		count := int(window.To.Sub(window.From) / time.Second)
		if intent.StepSeconds < 60 || count%intent.StepSeconds != 0 || count/intent.StepSeconds > intent.Limit || count/intent.StepSeconds > 10 || len(intent.Dimensions) != 1 || intent.Dimensions[0] != "time" {
			return p, ErrInvalid
		}
	} else if intent.StepSeconds != 0 || !compare && len(intent.Dimensions) != 0 {
		return p, ErrInvalid
	}
	found := false
	for _, proof := range n.qualifications {
		op := proof.Operation
		if op == "" {
			op = "aggregate"
		}
		if proof.DatasetID != intent.DatasetID || op != intent.Operation || proof.SourceRevision != b.Revision || proof.BindingDigest != evidence.BindingScopeDigest(b) || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(proof.EvidenceDigest) || proof.CollectorVersion == "" || !proof.VerifiedUntil.After(time.Now()) || !slices.Contains([]string{"c", "s", "c-p", "s-p", "c-app", "s-app", "app"}, proof.ObservationPoint) || proof.MaxWindowSeconds < 1 {
			continue
		}
		from := window.From
		if compare {
			from = intent.ComparisonTimeRange.From
		}
		if from.Before(proof.CoverageFrom) || window.To.After(proof.CoverageTo) || window.To.Sub(window.From) > time.Duration(proof.MaxWindowSeconds)*time.Second {
			continue
		}
		if found {
			return p, ErrInvalid
		}
		found = true
		p.proof = proof
	}
	if !found {
		return p, ErrSource
	}
	p.intent = intent
	p.scope = scope
	p.catalogDigest = catalogDigest(catalog)
	p.digest = graph.ScopeDigest(struct {
		Intent           QueryIntent
		Scope            graph.Scope
		Proof            Qualification
		Binding, Catalog string
	}{intent, scope, p.proof, evidence.BindingScopeDigest(b), p.catalogDigest})
	return p, nil
}

// RawQueryCost is also used by the MCP admission before any backend query.
// It delegates to the unified cost function so network and Victoria expansion
// can never drift apart; an intent that cannot be bounded conservatively counts
// as one request.
func NetworkRawQueryCost(intent QueryIntent) int64 {
	cost, err := SourceRequestCost(intent)
	if err != nil {
		return 1
	}
	return cost
}

func (n *Network) Execute(ctx context.Context, p NetworkPlan) (QueryResult, []evidence.Evidence, error) {
	out := QueryResult{SchemaVersion: "query-result/v1", DatasetID: p.dataset.ID, PlanDigest: p.digest, CatalogDigest: p.catalogDigest, ScopeDigest: graph.ScopeDigest(p.scope), Status: "complete", Exact: p.proof.CoverageComplete, Unit: p.dataset.Unit, Rows: []Row{}, SnapshotReadAt: time.Now().UTC(), Freshness: "fresh", Warnings: []string{}, DegradedSources: []string{}, TimeRange: copyQueryTimeRange(p.intent.TimeRange), ComparisonTimeRange: copyQueryTimeRange(p.intent.ComparisonTimeRange), StepSeconds: p.intent.StepSeconds, DerivationEvidenceRefs: []string{}, Statistics: []Statistic{}}
	facts := []evidence.Evidence{}
	if p.digest == "" || !p.proof.VerifiedUntil.After(time.Now()) {
		return out, facts, ErrSource
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	b := n.adapter.Binding()
	out.SourceManifest = &SourceManifest{SourceID: b.SourceID, Revision: b.Revision, BindingDigest: evidence.BindingScopeDigest(b), QualificationDigest: graph.ScopeDigest(p.proof), TemplateVersion: "deepflow-network-summary/v1", CoverageFrom: p.intent.TimeRange.From, CoverageTo: p.intent.TimeRange.To}
	from := p.intent.TimeRange.From
	if p.intent.ComparisonTimeRange != nil {
		from = p.intent.ComparisonTimeRange.From
		out.Dimension = "comparison"
		out.SourceManifest.CoverageFrom = from
	}
	windows := []*QueryTimeRange{}
	if p.intent.Operation == "trend" {
		for at := from; at.Before(p.intent.TimeRange.To); at = at.Add(time.Duration(p.intent.StepSeconds) * time.Second) {
			windows = append(windows, &QueryTimeRange{at, at.Add(time.Duration(p.intent.StepSeconds) * time.Second), p.intent.TimeRange.Timezone})
		}
	} else {
		if p.intent.ComparisonTimeRange != nil {
			windows = append(windows, p.intent.ComparisonTimeRange)
		}
		windows = append(windows, p.intent.TimeRange)
	}
	queries := []deepflow.SummaryQuery{}
	for _, w := range windows {
		queries = append(queries, deepflow.SummaryQuery{Target: p.intent.NetworkSelection.TargetCanonicalID, Peer: p.intent.NetworkSelection.PeerCanonicalID, Direction: p.intent.NetworkSelection.Direction, ObservationPoint: p.proof.ObservationPoint, From: w.From, To: w.To, Scope: p.scope, L7: strings.HasPrefix(p.dataset.ID, "network.http")})
	}
	summaries, results, err := n.adapter.SummaryBatch(ctx, queries)
	if err != nil {
		return out, facts, err
	}
	index := 0
	hashes := []string{}
	query := func(window *QueryTimeRange, label string) (float64, bool, error) {
		summary, result := summaries[index], results[index]
		index++
		out.Warnings = append(out.Warnings, result.Warnings...)
		out.DegradedSources = append(out.DegradedSources, result.DegradedSources...)
		if result.Partial || result.Freshness != "fresh" {
			out.Exact = false
			out.Status = "degraded"
			out.Freshness = result.Freshness
			return 0, false, nil
		}
		hashes = append(hashes, result.QueryHash)
		out.NetworkContext = &NetworkContext{Selection: *p.intent.NetworkSelection, ObservationPoint: p.proof.ObservationPoint, Population: summary.Population, TimeReliable: summary.TimeReliable, CoverageComplete: p.proof.CoverageComplete, Protocol: summary.Protocol}
		facts = append(facts, result.Evidence...)
		for _, e := range result.Evidence {
			out.DerivationEvidenceRefs = append(out.DerivationEvidenceRefs, e.EvidenceID)
		}
		out.Statistics = append(out.Statistics, Statistic{Key: label + ".observations", Value: float64(summary.Observations), Unit: "observations"})
		v := float64(0)
		valid := true
		switch p.dataset.ID {
		case "network.http-responses":
			v = float64(summary.Responses)
		case "network.http-5xx":
			v = float64(summary.Errors)
		case "network.http-5xx-rate", "network.http-summary":
			if summary.Responses == 0 {
				valid = false
			} else {
				v = 100 * float64(summary.Errors) / float64(summary.Responses)
			}
		case "network.http-response-time":
			if summary.Responses == 0 {
				valid = false
			} else {
				v = float64(summary.DurationMicroseconds) / float64(summary.Responses) / 1000
			}
		case "network.tcp-retransmissions":
			v = float64(summary.Retransmissions)
		case "network.connection-failure-observations", "network.tcp-summary":
			v = float64(summary.Failures)
		default:
			return 0, false, ErrUnsupported
		}
		if summary.Protocol == "http" {
			out.Statistics = append(out.Statistics, Statistic{label + ".responses", float64(summary.Responses), "responses"}, Statistic{label + ".5xx", float64(summary.Errors), "responses"})
			if p.dataset.ID == "network.http-summary" && summary.Responses > 0 {
				out.Statistics = append(out.Statistics, Statistic{label + ".mean_response_ms", float64(summary.DurationMicroseconds) / float64(summary.Responses) / 1000, "milliseconds"})
			}
		} else if p.dataset.ID == "network.tcp-summary" {
			out.Statistics = append(out.Statistics, Statistic{label + ".retransmissions", float64(summary.Retransmissions), "retransmissions"}, Statistic{label + ".failure_observations", float64(summary.Failures), "observations"})
		}
		if !valid {
			out.Warnings = append(out.Warnings, label+"_denominator_zero")
			out.Exact = false
			return 0, false, nil
		}
		out.Rows = append(out.Rows, Row{label, v})
		return v, true, nil
	}
	if p.intent.Operation == "trend" {
		out.Dimension = "time"
		for at := from; at.Before(p.intent.TimeRange.To); at = at.Add(time.Duration(p.intent.StepSeconds) * time.Second) {
			w := &QueryTimeRange{at, at.Add(time.Duration(p.intent.StepSeconds) * time.Second), p.intent.TimeRange.Timezone}
			if _, _, err := query(w, at.Format(time.RFC3339)); err != nil {
				return out, facts, err
			}
		}
	} else {
		baseline, baseOK := float64(0), false
		if p.intent.ComparisonTimeRange != nil {
			var err error
			baseline, baseOK, err = query(p.intent.ComparisonTimeRange, "baseline")
			if err != nil {
				return out, facts, err
			}
		}
		current, currentOK, err := query(p.intent.TimeRange, "current")
		if err != nil {
			return out, facts, err
		}
		if p.intent.ComparisonTimeRange != nil && baseOK && currentOK && out.Exact {
			delta := current - baseline
			direction := "unchanged"
			if delta > 0 {
				direction = "increase"
			}
			if delta < 0 {
				direction = "decrease"
			}
			if p.intent.Operation == "compare_percent" {
				out.Rows = []Row{}
				out.Unit = "percent"
				if baseline == 0 {
					out.Exact = false
					out.Warnings = append(out.Warnings, "percentage_requires_nonzero_baseline")
				} else {
					out.Rows = append(out.Rows, Row{direction, math.Abs(delta) / baseline * 100})
				}
			} else {
				out.Rows = append(out.Rows, Row{direction, math.Abs(delta)})
			}
		}
	}
	if len(out.Rows) == 0 {
		out.Status = "no_data"
	} else if !out.Exact && out.Status == "complete" {
		out.Status = "partial"
	}
	if !p.proof.CoverageComplete {
		out.Warnings = append(out.Warnings, "collector_coverage_unverified")
	}
	out.SourceManifest.QueryHash = graph.ScopeDigest(hashes)
	if !p.proof.VerifiedUntil.After(time.Now()) {
		return out, facts, ErrSource
	}
	return out, facts, nil
}

func (n *Network) Capabilities(ctx context.Context, scope graph.Scope) ([]SourceCapability, error) {
	entries := []SourceCapability{}
	if _, err := n.adapter.ScopedCapabilities(ctx, scope); err != nil {
		return entries, err
	}
	catalog, err := Catalog()
	if err != nil {
		return entries, err
	}
	b := n.adapter.Binding()
	for _, d := range catalog.Datasets {
		if d.Source != "deepflow" {
			continue
		}
		for _, q := range n.qualifications {
			op := q.Operation
			if op == "" {
				op = "aggregate"
			}
			if q.DatasetID == d.ID && slices.Contains(d.Operations, op) && q.SourceRevision == b.Revision && q.BindingDigest == evidence.BindingScopeDigest(b) && q.VerifiedUntil.After(time.Now()) && q.MaxWindowSeconds > 0 && q.CoverageTo.After(q.CoverageFrom) && q.CollectorVersion != "" && regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(q.EvidenceDigest) && slices.Contains([]string{"c", "s", "c-p", "s-p", "c-app", "s-app", "app"}, q.ObservationPoint) {
				entries = append(entries, SourceCapability{b.SourceID, "deepflow", b.Revision, d.ID, op, q.CollectorVersion, graph.ScopeDigest(q), q.CoverageFrom, q.CoverageTo, q.VerifiedUntil, q.MaxWindowSeconds, "qualification_configured"})
			}
		}
	}
	return entries, nil
}
