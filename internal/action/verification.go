package action

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"ops-platform/internal/persistence"
	"ops-platform/internal/resource"
	"slices"
)

const VerificationMount = "/run/ops-probe"
const VerificationSubPath = ".ops-verification"

type VerificationSpec struct {
	Kind               string `json:"kind"`
	Port               int    `json:"port,omitempty"`
	Path               string `json:"path,omitempty"`
	HTTPS              bool   `json:"https,omitempty"`
	ExpectedStatus     int    `json:"expectedStatus,omitempty"`
	ExpectedBodyDigest string `json:"expectedBodyDigest,omitempty"`
}

func (v VerificationSpec) Intent() string { return "ops-verify " + v.Kind }
func (v VerificationSpec) Valid(target string) bool {
	id, err := resource.ParseCanonicalID(target)
	if err != nil || id.Domain != "k8s" || id.APIGroup != "core" {
		return false
	}
	if v.Kind == "pvc_read_write" {
		return id.Kind == "PersistentVolumeClaim" && v.Port == 0 && v.Path == "" && !v.HTTPS && v.ExpectedStatus == 0 && v.ExpectedBodyDigest == ""
	}
	if id.Kind != "Service" || v.Port < 1 || v.Port > 65535 {
		return false
	}
	if v.Kind == "tcp" {
		return v.Path == "" && !v.HTTPS && v.ExpectedStatus == 0 && v.ExpectedBodyDigest == ""
	}
	if v.Kind != "http" || v.ExpectedStatus < 200 || v.ExpectedStatus > 599 || len(v.Path) > 1024 {
		return false
	}
	path, err := url.ParseRequestURI(v.Path)
	return err == nil && strings.HasPrefix(v.Path, "/") && !strings.HasPrefix(v.Path, "//") && path.Host == "" && path.RawQuery == "" && path.Fragment == "" && path.Scheme == "" && (v.ExpectedBodyDigest == "" || validSHA256(v.ExpectedBodyDigest))
}
func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil && value == strings.ToLower(value)
}

type VerificationResult struct {
	SchemaVersion string    `json:"schemaVersion"`
	ExecutionID   uuid.UUID `json:"executionId"`
	RequestDigest string    `json:"executionRequestDigest"`
	TargetUID     string    `json:"targetUid"`
	Kind          string    `json:"kind"`
	Vantage       string    `json:"vantage"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	Verdict       string    `json:"verdict"`
	Reason        string    `json:"reason"`
	HTTPStatus    int       `json:"httpStatus,omitempty"`
	BodyDigest    string    `json:"bodyDigest,omitempty"`
	Cleanup       bool      `json:"cleanup"`
}

func (r VerificationResult) Valid(e Execution) bool {
	if e.Binding.Verification == nil || e.StartedAt == nil || r.SchemaVersion != "command-verification/v1" || r.ExecutionID != e.ID || r.RequestDigest != e.RequestDigest || r.Kind != e.Binding.Verification.Kind || r.TargetUID != e.Binding.TargetUID || r.Vantage != "runner/"+e.Binding.VerificationNamespace || r.StartedAt.Before(*e.StartedAt) || r.FinishedAt.Before(r.StartedAt) || r.FinishedAt.After(time.Now().Add(time.Second)) {
		return false
	}
	switch r.Reason {
	case "tcp_connected", "http_assertions_met", "read_write_verified":
		if r.Verdict != "resolved" {
			return false
		}
	case "connection_refused", "http_status_mismatch", "http_body_mismatch", "read_back_mismatch":
		if r.Verdict != "not_resolved" {
			return false
		}
	case "probe_unavailable", "probe_timeout", "identity_changed", "cleanup_failed":
		if r.Verdict != "inconclusive" {
			return false
		}
	default:
		return false
	}
	common := slices.Contains([]string{"probe_unavailable", "probe_timeout", "identity_changed"}, r.Reason)
	allowed := map[string][]string{"tcp": {"tcp_connected", "connection_refused"}, "http": {"http_assertions_met", "http_status_mismatch", "http_body_mismatch", "connection_refused"}, "pvc_read_write": {"read_write_verified", "read_back_mismatch", "cleanup_failed"}}
	if !common && !slices.Contains(allowed[r.Kind], r.Reason) {
		return false
	}
	if r.Kind != "http" && (r.HTTPStatus != 0 || r.BodyDigest != "") {
		return false
	}
	if r.Reason == "http_status_mismatch" && (r.HTTPStatus == 0 || r.HTTPStatus == e.Binding.Verification.ExpectedStatus) {
		return false
	}
	if r.Reason == "http_body_mismatch" && (e.Binding.Verification.ExpectedBodyDigest == "" || r.BodyDigest == e.Binding.Verification.ExpectedBodyDigest || !validSHA256(r.BodyDigest)) {
		return false
	}
	if r.Reason == "cleanup_failed" && r.Cleanup {
		return false
	}
	if r.BodyDigest != "" && !validSHA256(r.BodyDigest) || r.HTTPStatus < 0 || r.HTTPStatus > 599 {
		return false
	}
	if r.Kind == "pvc_read_write" && r.Verdict == "resolved" && !r.Cleanup {
		return false
	}
	if r.Kind == "tcp" && r.Verdict == "resolved" && r.Reason != "tcp_connected" || r.Kind == "http" && r.Verdict == "resolved" && r.Reason != "http_assertions_met" || r.Kind == "pvc_read_write" && r.Verdict == "resolved" && r.Reason != "read_write_verified" {
		return false
	}
	if r.Kind == "http" && r.Verdict == "resolved" && (r.HTTPStatus != e.Binding.Verification.ExpectedStatus || !validSHA256(r.BodyDigest) || e.Binding.Verification.ExpectedBodyDigest != "" && r.BodyDigest != e.Binding.Verification.ExpectedBodyDigest) {
		return false
	}
	return true
}

// Probe endpoints come from a freshly authenticated Kubernetes object, never a
// user-supplied host. No proxy, redirect, arbitrary method or credential is sent.
func probeService(ctx context.Context, spec VerificationSpec, object unstructured.Unstructured) (string, string, int, string) {
	address, _, _ := unstructured.NestedString(object.Object, "spec", "clusterIP")
	ip, err := netip.ParseAddr(address)
	if err != nil || !ip.IsGlobalUnicast() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return "inconclusive", "probe_unavailable", 0, ""
	}
	found := false
	ports, _, _ := unstructured.NestedSlice(object.Object, "spec", "ports")
	for _, raw := range ports {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		protocol, _ := p["protocol"].(string)
		if (protocol == "" || protocol == "TCP") && p["port"] == int64(spec.Port) {
			found = true
		}
	}
	if !found {
		return "inconclusive", "probe_unavailable", 0, ""
	}
	return runNetworkProbe(ctx, spec, net.JoinHostPort(ip.String(), itoaPort(spec.Port)), object.GetName()+"."+object.GetNamespace()+".svc")
}
func itoaPort(port int) string { return strconv.Itoa(port) }
func networkFailure(err error) (string, string) {
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "not_resolved", "connection_refused"
	}
	var timeout net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &timeout) && timeout.Timeout() {
		return "inconclusive", "probe_timeout"
	}
	return "inconclusive", "probe_unavailable"
}
func runNetworkProbe(ctx context.Context, spec VerificationSpec, address, serverName string) (string, string, int, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	if spec.Kind == "tcp" {
		connection, err := dialer.DialContext(ctx, "tcp", address)
		if err != nil {
			v, r := networkFailure(err)
			return v, r, 0, ""
		}
		_ = connection.Close()
		return "resolved", "tcp_connected", 0, ""
	}
	scheme := "http"
	if spec.HTTPS {
		scheme = "https"
	}
	transport := &http.Transport{Proxy: nil, DialContext: dialer.DialContext, DisableKeepAlives: true, DisableCompression: true, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, "GET", scheme+"://"+address+spec.Path, nil)
	if err != nil {
		return "inconclusive", "probe_unavailable", 0, ""
	}
	request.Host = serverName
	response, err := client.Do(request)
	if err != nil {
		v, r := networkFailure(err)
		return v, r, 0, ""
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(body) > 64<<10 {
		return "inconclusive", "probe_unavailable", response.StatusCode, ""
	}
	digest := Digest(body)
	clear(body)
	if response.StatusCode != spec.ExpectedStatus {
		return "not_resolved", "http_status_mismatch", response.StatusCode, digest
	}
	if spec.ExpectedBodyDigest != "" && digest != spec.ExpectedBodyDigest {
		return "not_resolved", "http_body_mismatch", response.StatusCode, digest
	}
	return "resolved", "http_assertions_met", response.StatusCode, digest
}

// OpenRoot confines access; O_EXCL prevents following a pre-existing symlink or
// overwriting a business file. Only this nonce-named entry is ever removed.
func probeReadWrite(ctx context.Context, directory string, executionID uuid.UUID) (verdict, reason string, cleanup bool) {
	verdict, reason = "inconclusive", "probe_unavailable"
	if executionID == uuid.Nil {
		return
	}
	if ctx.Err() != nil {
		return "inconclusive", "probe_timeout", true
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return
	}
	defer root.Close()
	name := "probe-" + executionID.String()
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return
	}
	defer func() {
		closeErr := file.Close()
		removeErr := root.Remove(name)
		if closeErr != nil || removeErr != nil {
			verdict, reason, cleanup = "inconclusive", "cleanup_failed", false
		} else {
			cleanup = true
		}
	}()
	if ctx.Err() != nil {
		reason = "probe_timeout"
		return
	}
	payload := make([]byte, 4096)
	if _, err = rand.Read(payload); err != nil {
		return
	}
	defer clear(payload)
	if _, err = file.Write(payload); err != nil {
		return
	}
	if err = file.Sync(); err != nil {
		return
	}
	if _, err = file.Seek(0, 0); err != nil {
		return
	}
	actual, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil {
		return
	}
	defer clear(actual)
	if ctx.Err() != nil {
		reason = "probe_timeout"
		return
	}
	if sha256.Sum256(actual) != sha256.Sum256(payload) {
		return "not_resolved", "read_back_mismatch", false
	}
	return "resolved", "read_write_verified", false
}
func verificationObject(ctx context.Context, c KubeCredential, b Binding, name string) (unstructured.Unstructured, error) {
	var object unstructured.Unstructured
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(c.CA) {
		return object, ErrDenied
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	plural := "services"
	if b.Verification.Kind == "pvc_read_write" {
		plural = "persistentvolumeclaims"
	}
	if b.Namespace == nil || !namePattern.MatchString(name) {
		return object, ErrInvalid
	}
	request, err := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(c.Endpoint, "/")+"/api/v1/namespaces/"+url.PathEscape(*b.Namespace)+"/"+plural+"/"+url.PathEscape(name), nil)
	if err != nil {
		return object, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+c.Token)
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		return object, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return object, ErrDenied
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &object) != nil {
		return object, ErrInvalid
	}
	id, err := resource.ParseCanonicalID(b.Target)
	if err != nil || object.GetAPIVersion() != "v1" || object.GetKind() != id.Kind || object.GetName() != name || object.GetNamespace() != *b.Namespace || string(object.GetUID()) != b.TargetUID {
		return object, ErrDenied
	}
	return object, nil
}
func (c RunnerClient) runVerification(ctx context.Context, claim ClaimedCommand) error {
	b := claim.VerificationBinding
	if b == nil || b.Verification == nil || claim.Credentials.Kubernetes == nil || !b.Verification.Valid(b.Target) || claim.Command != b.Verification.Intent() || os.Getenv("OPS_VERIFICATION_NAMESPACE") != b.VerificationNamespace || claim.VerificationTargetName != b.VerificationTargetName || b.Namespace == nil || claim.Credentials.Kubernetes.Namespace != *b.Namespace {
		return ErrDenied
	}
	result := VerificationResult{SchemaVersion: "command-verification/v1", ExecutionID: c.ExecutionID, RequestDigest: b.Digest(), TargetUID: b.TargetUID, Kind: b.Verification.Kind, Vantage: "runner/" + b.VerificationNamespace, StartedAt: time.Now().UTC(), Verdict: "inconclusive", Reason: "probe_unavailable"}
	object, err := verificationObject(ctx, *claim.Credentials.Kubernetes, *b, claim.VerificationTargetName)
	if err == nil {
		if b.Verification.Kind == "pvc_read_write" {
			phase, _, _ := unstructured.NestedString(object.Object, "status", "phase")
			if phase == "Bound" && object.GetAnnotations()["ops.platform/verification-only"] == "true" {
				result.Verdict, result.Reason, result.Cleanup = probeReadWrite(ctx, VerificationMount, c.ExecutionID)
			}
		} else {
			result.Verdict, result.Reason, result.HTTPStatus, result.BodyDigest = probeService(ctx, *b.Verification, object)
		}
		after, err := verificationObject(ctx, *claim.Credentials.Kubernetes, *b, claim.VerificationTargetName)
		if err != nil || after.GetResourceVersion() != object.GetResourceVersion() {
			result.Verdict, result.Reason = "inconclusive", "identity_changed"
		}
	}
	result.FinishedAt = time.Now().UTC()
	raw := Canonical(result)
	if err := c.call(ctx, "output", OutputChunk{ExecutionID: c.ExecutionID, Stream: "stdout", Seq: 1, Bytes: raw, ObservedAt: result.FinishedAt}, nil); err != nil {
		return err
	}
	exit := 0
	if result.Verdict != "resolved" {
		exit = 1
	}
	return c.call(ctx, "finish", map[string]any{"exitCode": exit, "finalSeq": 1, "truncated": false, "verification": result}, nil)
}

// Verification output is an authenticated execution attestation, not fabricated
// Kubernetes/DeepFlow Evidence. Its exact bytes must have a verified output archive.
func verificationResultTx(ctx context.Context, tx pgx.Tx, e Execution) (*VerificationResult, error) {
	if e.Binding.Verification == nil {
		return nil, nil
	}
	if e.ArchiveRef == nil || e.OutputDigest == nil || e.Truncated || e.CompletedAt == nil {
		return nil, ErrDenied
	}
	var verified bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM action.output_archives WHERE tenant_id=$1 AND execution_id=$2 AND verified_at IS NOT NULL)`, e.Binding.TenantID, e.ID).Scan(&verified); err != nil || !verified {
		return nil, ErrDenied
	}
	p, _, err := profileTx(ctx, tx, e.Binding.TenantID, e.Binding.ProfileID, e.Binding.ProfileVersion)
	if err != nil || !slices.Contains(p.Tools, "ops-verify") {
		return nil, ErrDenied
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT payload FROM action.events WHERE tenant_id=$1 AND execution_id=$2 AND event_type='state' AND payload->'verification' IS NOT NULL ORDER BY event_seq DESC LIMIT 1`, e.Binding.TenantID, e.ID).Scan(&raw); err != nil {
		return nil, err
	}
	var payload struct {
		Verification *VerificationResult `json:"verification"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Verification == nil || !payload.Verification.Valid(e) || payload.Verification.FinishedAt.After(*e.CompletedAt) {
		return nil, ErrInvalid
	}
	if e.Binding.RelatedExecutionID == nil {
		return nil, ErrDenied
	}
	var parentCompleted time.Time
	if err := tx.QueryRow(ctx, `SELECT completed_at FROM action.executions WHERE tenant_id=$1 AND execution_id=$2 AND state IN('succeeded','failed')`, e.Binding.TenantID, *e.Binding.RelatedExecutionID).Scan(&parentCompleted); err != nil || payload.Verification.StartedAt.Before(parentCompleted) {
		return nil, ErrDenied
	}
	return payload.Verification, nil
}
func (s Service) VerificationResult(ctx context.Context, tenant, id uuid.UUID) (result *VerificationResult, err error) {
	err = persistence.WithTenantTx(ctx, s.Pool, tenant, func(tx pgx.Tx) error {
		e, err := executionTx(ctx, tx, tenant, id)
		if err != nil {
			return err
		}
		result, err = verificationResultTx(ctx, tx, e)
		return err
	})
	return
}
