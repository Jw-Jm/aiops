package observationquery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"log/slog"
	"math"
	"ops-platform/internal/analytics"
	"ops-platform/internal/contract"
	"ops-platform/internal/graph"
	"ops-platform/internal/persistence"
	"time"
)

type Capability struct {
	Name             string    `json:"name"`
	Source           Source    `json:"source"`
	CoverageFrom     time.Time `json:"coverageFrom"`
	CoverageTo       time.Time `json:"coverageTo"`
	VerifiedUntil    time.Time `json:"verifiedUntil"`
	MaxWindowSeconds int64     `json:"maxWindowSeconds"`
}
type Service struct {
	NativeLogs *KubernetesLogs
	Repository Repository
	Adapters   map[string]*analytics.Victoria
	// Resources comes from the currently owned, qualified graph snapshot.
	Resources     func(context.Context, graph.Scope) ([]Resource, error)
	CheckResource func(context.Context, Resource) error
}

func (s Service) Capabilities(ctx context.Context, claims graph.Claims) ([]Capability, error) {
	return s.capabilities(ctx, claims, nil)
}
func (s Service) capabilities(ctx context.Context, claims graph.Claims, resources []Resource) ([]Capability, error) {
	_, err := (graph.Authorization{Pool: s.Repository.Pool}).EffectiveClaim(ctx, claims)
	if err != nil {
		return nil, ErrDenied
	}
	out := []Capability{}
	if s.NativeLogs != nil && len(claims.Scope.Resources) == 1 {
		if resources == nil {
			resources, err = s.Resources(ctx, claims.Scope)
			if err != nil {
				return nil, err
			}
		}
		for _, target := range resources {
			if target.CanonicalID != claims.Scope.Resources[0] {
				continue
			}
			entry, e := s.NativeLogs.Capability(ctx, claims.Scope, target)
			if e != nil {
				continue
			}
			e = persistence.WithTenantTx(ctx, s.Repository.Pool, uuid.MustParse(claims.Scope.Tenant), func(tx pgx.Tx) error {
				return tx.QueryRow(ctx, `SELECT credential_revision FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, claims.Scope.Tenant, entry.Source.RegistrationID, entry.Source.RegistrationRevision).Scan(&entry.Source.CredentialRevision)
			})
			if e == nil {
				out = append(out, entry)
			}
		}
	}
	for id, adapter := range s.Adapters {
		entries, err := adapter.Capabilities(ctx, claims.Scope)
		if err != nil {
			continue
		}
		var samples, aggregate *analytics.SourceCapability
		for i := range entries {
			e := &entries[i]
			if e.DatasetID != "logs.records" {
				continue
			}
			if e.Operation == "samples" {
				samples = e
			}
			if e.Operation == "aggregate" {
				aggregate = e
			}
		}
		if samples == nil || aggregate == nil {
			continue
		}
		sid, err := uuid.Parse(id)
		if err != nil {
			return nil, ErrUnavailable
		}
		entry := Capability{Source: Source{RegistrationID: sid, RegistrationRevision: samples.Revision, QualificationDigest: graph.ScopeDigest([]analytics.SourceCapability{*samples, *aggregate}), CollectorVersion: samples.CollectorVersion}, CoverageFrom: samples.CoverageFrom, CoverageTo: samples.CoverageTo, VerifiedUntil: samples.VerifiedUntil, MaxWindowSeconds: min(samples.MaxWindowSeconds, aggregate.MaxWindowSeconds)}
		if aggregate.CoverageFrom.After(entry.CoverageFrom) {
			entry.CoverageFrom = aggregate.CoverageFrom
		}
		if aggregate.CoverageTo.Before(entry.CoverageTo) {
			entry.CoverageTo = aggregate.CoverageTo
		}
		if aggregate.VerifiedUntil.Before(entry.VerifiedUntil) {
			entry.VerifiedUntil = aggregate.VerifiedUntil
		}
		err = persistence.WithTenantTx(ctx, s.Repository.Pool, uuid.MustParse(claims.Scope.Tenant), func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT credential_revision,instance_key FROM platform.source_registrations WHERE tenant_id=$1 AND source_id=$2 AND revision=$3 AND status='active'`, claims.Scope.Tenant, sid, samples.Revision).Scan(&entry.Source.CredentialRevision, &entry.Name)
		})
		if err != nil {
			continue
		}
		out = append(out, entry)
	}
	return out, nil
}
func (s Service) Validate(ctx context.Context, claims graph.Claims, request Request) error {
	resources, err := s.Resources(ctx, claims.Scope)
	if err != nil {
		return err
	}
	return s.validateBindings(ctx, claims, request, resources)
}
func (s Service) validateBindings(ctx context.Context, claims graph.Claims, request Request, resources []Resource) error {
	if s.Adapters[request.Source.RegistrationID.String()] == nil && (s.NativeLogs == nil || s.NativeLogs.SourceID != request.Source.RegistrationID) {
		return ErrUnavailable
	}
	var err error
	found := false
	for _, r := range resources {
		if graph.ScopeDigest(r) == graph.ScopeDigest(request.Resource) {
			found = true
			break
		}
	}
	if !found {
		return ErrDenied
	}
	scoped := claims
	scoped.Scope, err = analytics.RestrictScope(claims.Scope, request.Selection())
	if err != nil {
		return ErrDenied
	}
	scoped.GrantSnapshotDigest = graph.ScopeDigest(claims.Scope)
	capabilities, err := s.capabilities(ctx, scoped, resources)
	if err != nil {
		return err
	}
	for _, c := range capabilities {
		if c.Source == request.Source && !request.Window.From.Before(c.CoverageFrom) && !request.Window.To.After(c.CoverageTo) && request.Window.To.Sub(request.Window.From) <= time.Duration(c.MaxWindowSeconds)*time.Second {
			return nil
		}
	}
	return ErrUnavailable
}
func (s Service) Create(ctx context.Context, claims graph.Claims, request Request, key uuid.UUID) (View, error) {
	resources, err := s.Resources(ctx, claims.Scope)
	if err != nil {
		return View{}, err
	}
	if err = s.validateBindings(ctx, claims, request, resources); err != nil {
		return View{}, err
	}
	for _, candidate := range resources {
		if graph.ScopeDigest(candidate) == graph.ScopeDigest(request.Resource) {
			if err = s.CheckResource(ctx, candidate); err != nil {
				return View{}, err
			}
			return s.Repository.Create(ctx, claims, request, key)
		}
	}
	return View{}, ErrDenied
}

func (s Service) Read(ctx context.Context, claims graph.Claims, id uuid.UUID) (Record, error) {
	rec, err := s.Repository.Read(ctx, claims, id)
	if err != nil || rec.State == "expired" {
		return rec, err
	}
	if err = s.Validate(ctx, claims, rec.Request); err != nil {
		return Record{}, err
	}
	// The qualification/graph read above may have raced a revocation. This final
	// transaction checks the session and source again before exposing stored data.
	return s.Repository.Read(ctx, claims, id)
}
func (s Service) List(ctx context.Context, claims graph.Claims) ([]View, error) {
	page, err := s.ListPage(ctx, claims, uuid.Nil, 50)
	return page.Items, err
}
func (s Service) ListPage(ctx context.Context, claims graph.Claims, cursor uuid.UUID, limit int) (HistoryPage, error) {
	page := HistoryPage{Items: []View{}}
	if limit < 1 || limit > 50 {
		return page, ErrInvalid
	}
	resources, err := s.Resources(ctx, claims.Scope)
	if err != nil {
		return page, err
	}
	ids, err := s.Repository.IDs(ctx, claims, cursor, limit)
	if err != nil {
		return page, err
	}
	if len(ids) > limit {
		ids = ids[:limit]
		last := ids[len(ids)-1]
		page.NextCursor = &last
	}
	for _, id := range ids {
		rec, err := s.Repository.Read(ctx, claims, id)
		if err == nil && rec.State == "expired" {
			continue
		}
		if err == nil {
			err = s.validateBindings(ctx, claims, rec.Request, resources)
		}
		if errors.Is(err, ErrDenied) || errors.Is(err, ErrUnavailable) || errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return page, err
		}
		rec, err = s.Repository.Read(ctx, claims, id)
		if err != nil {
			return page, err
		}
		if rec.State != "expired" {
			page.Items = append(page.Items, rec.View)
		}
	}
	return page, nil
}
func (s Service) RunOne(ctx context.Context, tenant uuid.UUID, cluster string) error {
	rec, err := s.Repository.Claim(ctx, tenant, cluster)
	if err != nil {
		return err
	}
	if rec.State != "running" {
		return nil
	}
	claims := graph.Claims{Source: graph.SourceNativeInteractive, Native: &graph.NativeProof{SessionID: rec.SessionID.String()}, Subject: rec.OwnerSubject, Scope: rec.Scope}
	bounded, cancel := context.WithTimeout(ctx, time.Duration(rec.Request.Budget.MaxDurationSeconds)*time.Second)
	defer cancel()
	monitorDone := make(chan struct{})
	monitorStopped := make(chan struct{})
	go func() {
		defer close(monitorStopped)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorDone:
				return
			case <-bounded.Done():
				return
			case <-ticker.C:
				stopped, err := s.Repository.Cancelled(bounded, rec)
				if err != nil || stopped {
					cancel()
					return
				}
			}
		}
	}()
	result, runError := s.execute(bounded, claims, rec)
	if runError != nil {
		slog.Warn("manual query execution unavailable", "queryId", rec.QueryID, "error", runError)
	}
	close(monitorDone)
	<-monitorStopped
	// Cancellation of an upstream HTTP request is not proof that its server
	// stopped. Claim will record a termination receipt with sourceStopped=false.
	finish, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	return s.Repository.Finish(finish, rec, claims, result, runError)
}
func (s Service) execute(ctx context.Context, claims graph.Claims, rec Record) (json.RawMessage, error) {
	request := rec.Request
	current, err := (graph.Authorization{Pool: s.Repository.Pool}).EffectiveClaim(ctx, claims)
	if err != nil {
		return nil, ErrDenied
	}
	if err := s.Validate(ctx, claims, request); err != nil {
		return nil, err
	}
	if s.NativeLogs != nil && request.Source.RegistrationID == s.NativeLogs.SourceID {
		resources, err := s.Resources(ctx, claims.Scope)
		if err != nil {
			return nil, err
		}
		for _, target := range resources {
			if target.CanonicalID != request.Resource.CanonicalID {
				continue
			}
			samples, err := s.NativeLogs.Read(ctx, claims.Scope, target, request)
			if err != nil {
				return nil, err
			}
			if err = s.Validate(ctx, claims, request); err != nil {
				return nil, err
			}
			return buildResult(rec, "partial", map[string]any{"basis": "not_measured", "method": nil}, samples)
		}
		return nil, ErrDenied
	}
	adapter := s.Adapters[request.Source.RegistrationID.String()]
	if adapter == nil {
		return nil, ErrUnavailable
	}
	binding := analytics.Binding{Subject: claims.Subject, Grant: current, Selection: request.Selection()}
	aggregatePlan, err := adapter.Compile(request.Intent("aggregate"), binding)
	if err != nil {
		return nil, err
	}
	samplesPlan, err := adapter.Compile(request.Intent("samples"), binding)
	if err != nil {
		return nil, err
	}
	aggregate, err := adapter.Execute(ctx, aggregatePlan)
	if err != nil {
		return nil, err
	}
	samples, err := adapter.Execute(ctx, samplesPlan)
	if err != nil {
		return nil, err
	}
	if err = s.Validate(ctx, claims, request); err != nil {
		return nil, err
	}
	return BuildResult(rec, aggregate, samples)
}

// BuildResult uses only the measured aggregate as the population. An empty
// sample page alone never establishes a zero population.
func BuildResult(rec Record, aggregate, samples analytics.QueryResult) (json.RawMessage, error) {
	total := float64(0)
	for _, row := range aggregate.Rows {
		total += row.Value
	}
	if (!aggregate.Exact && !(aggregate.Status == "no_data" && len(aggregate.Rows) == 0 && len(aggregate.DegradedSources) == 0)) || math.IsNaN(total) || math.IsInf(total, 0) || total < 0 || total > 1e12 || total != math.Trunc(total) || len(samples.Samples) > rec.Request.Budget.MaxRows || float64(len(samples.Samples)) > total {
		return nil, ErrUnavailable
	}
	state := "partial"
	if total == 0 {
		state = "no_data"
	}
	return buildResult(rec, state, map[string]any{"basis": "exact", "method": "source_total", "count": int64(total)}, samples)
}
func buildResult(rec Record, state string, population map[string]any, samples analytics.QueryResult) (json.RawMessage, error) {
	if len(samples.Samples) > rec.Request.Budget.MaxRows {
		return nil, ErrUnavailable
	}
	items := samples.Samples
	if items == nil {
		items = []analytics.LogSample{}
	}
	reasons := []string{}
	if samples.Truncated {
		reason := "sample_limit"
		if rec.Source.CollectorVersion == NativeLogVersion {
			reason = "source_partial"
		}
		reasons = append(reasons, reason)
	}
	body := map[string]any{
		"schemaVersion": "ui-v12-observability-result/v1", "status": state, "resultKind": "log_samples", "exact": false,
		"resultId": uuid.New(), "queryId": rec.QueryID, "computedAt": time.Now().UTC(), "expiresAt": rec.ExpiresAt,
		"resource": rec.Resource, "source": rec.Source, "window": rec.Window, "coverage": rec.Coverage,
		"population": population,
		"samples":    map[string]any{"returned": len(items), "inferredPopulation": nil, "items": items}, "statistics": []any{},
		"truncation": map[string]any{"truncated": samples.Truncated, "reasons": reasons}, "warnings": append([]string{"bounded_sanitized_samples_not_total_count"}, samples.Warnings...),
	}
	body["resultDigest"] = graph.ScopeDigest(body)
	body["reauthorization"] = rec.Reauthorization
	raw, _ := json.Marshal(body)
	if len(raw) > rec.Request.Budget.MaxBytes || contract.Validate("https://ops.local/schemas/ui-v12-observability/v1#/$defs/resultView", raw) != nil {
		return nil, ErrUnavailable
	}
	return raw, nil
}
