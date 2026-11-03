package analytics

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/graph"
)

// The collector/material proof stays fixed; fresh ingestion bounds are renewed independently.
func (q Qualification) Digest() string {
	if q.Continuous {
		q.CoverageTo = time.Time{}
		q.VerifiedUntil = time.Time{}
	}
	return graph.ScopeDigest(q)
}
func (v *Victoria) currentQualifications() []Qualification {
	v.liveMu.RLock()
	defer v.liveMu.RUnlock()
	out := slices.Clone(v.qualifications)
	for i := range out {
		if out[i].Continuous {
			out[i].CoverageTo = v.liveTo
			out[i].VerifiedUntil = v.liveUntil
		}
	}
	return out
}
func (v *Victoria) ContinuousLogs() bool {
	if !v.binding.KubernetesLogArchive() {
		return false
	}
	for _, q := range v.qualifications {
		if q.Continuous {
			return true
		}
	}
	return false
}

// RefreshContinuousLogs opens only an actually stored, recent event watermark.
// It never advances a lease on a health probe, an empty response, or a future timestamp.
func (v *Victoria) RefreshContinuousLogs(ctx context.Context) (err error) {
	if !v.ContinuousLogs() {
		return ErrInvalid
	}
	defer func() {
		if err != nil {
			v.liveMu.Lock()
			v.liveUntil = time.Time{}
			v.liveMu.Unlock()
		}
	}()
	if v.authorize(ctx, v.binding) != nil {
		return ErrDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	now := time.Now().UTC()
	selectors := []string{}
	keys := []string{}
	for key := range v.binding.ScopeMapping.RequiredLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !safeLabel.MatchString(key) {
			return ErrDenied
		}
		selectors = append(selectors, v.binding.LogField(key)+":"+strconv.Quote(v.binding.ScopeMapping.RequiredLabels[key]))
	}
	namespaces := []string{}
	for _, ns := range v.binding.ScopeMapping.Scopes["namespace"] {
		namespaces = append(namespaces, v.binding.LogField("namespace")+":"+strconv.Quote(ns))
	}
	if len(namespaces) == 0 || len(v.binding.ScopeMapping.Scopes["cluster"]) != 1 {
		return ErrDenied
	}
	selectors = append(selectors, "("+strings.Join(namespaces, " OR ")+")", "cluster:"+strconv.Quote(v.binding.ScopeMapping.Scopes["cluster"][0]), "_time:["+now.Add(-time.Minute).Format(time.RFC3339Nano)+","+now.Format(time.RFC3339Nano)+"]")
	query := strings.Join(selectors, " AND ") + " | sort by (_time) desc | limit 1"
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(v.binding.Endpoint, "/")+"/select/logsql/query?"+url.Values{"query": {query}, "limit": {"1"}}.Encode(), nil)
	if err != nil {
		return ErrInvalid
	}
	response, err := v.client.Do(request)
	if err != nil {
		return ErrSource
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return ErrSource
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 {
		return ErrSource
	}
	var row map[string]string
	if json.Unmarshal(raw, &row) != nil {
		return ErrSource
	}
	for _, key := range keys {
		if row[v.binding.LogField(key)] != v.binding.ScopeMapping.RequiredLabels[key] {
			return ErrDenied
		}
	}
	if row["tenant"] != v.binding.Tenant || row["cluster"] != v.binding.ScopeMapping.Scopes["cluster"][0] || !slices.Contains(v.binding.ScopeMapping.Scopes["namespace"], row[v.binding.LogField("namespace")]) {
		return ErrDenied
	}
	if uuid.Validate(row[v.binding.LogField("uid")]) != nil {
		return ErrSource
	}
	at, err := time.Parse(time.RFC3339Nano, row["_time"])
	if err != nil || at.After(now) || at.Before(now.Add(-time.Minute)) {
		return ErrSource
	}
	if v.authorize(ctx, v.binding) != nil {
		return ErrDenied
	}
	v.liveMu.Lock()
	v.liveTo = at.Add(time.Nanosecond)
	v.liveUntil = now.Add(30 * time.Second)
	v.liveMu.Unlock()
	return nil
}
