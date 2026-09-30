package observability

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

const RedactedValue = "[REDACTED]"

var (
	bearerPattern        = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/-]+=*`)
	jwtPattern           = regexp.MustCompile(`\b[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	credentialPattern    = regexp.MustCompile(`(?i)\b(token|password|secret|api[_-]?key|client[_-]?secret)\s*[:=]\s*([^\s,;]+)`)
	urlCredentialPattern = regexp.MustCompile(`(?i)(https?|postgres(?:ql)?|mysql)://[^/\s:@]+:[^/\s@]+@`)
	privateKeyPattern    = regexp.MustCompile(`(?s)-----BEGIN [^-]*(?:PRIVATE KEY|SECRET)-----.*?-----END [^-]*(?:PRIVATE KEY|SECRET)-----`)
)

type correlationKey struct{}
type requestIDKey struct{}

// Correlation holds only bounded IDs. Arbitrary request data is not accepted
// as a correlation field and none of these values become metric labels.
type Correlation struct {
	RequestID          string
	FindingID          string
	IncidentID         string
	InvestigationJobID string
	ActionPlanID       string
	ExecutionID        string
}

func NewLogger(output io.Writer, level slog.Level) *slog.Logger {
	if output == nil {
		output = os.Stdout
	}
	base := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level})
	return slog.New(&redactingHandler{next: base})
}

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, requestID)
}

func RequestIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey{}).(string)
	return value
}

func WithCorrelation(ctx context.Context, correlation Correlation) context.Context {
	return context.WithValue(ctx, correlationKey{}, correlation)
}

func LoggerWithContext(ctx context.Context, logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = NewLogger(nil, slog.LevelInfo)
	}
	attrs := make([]any, 0, 7)
	correlation, _ := ctx.Value(correlationKey{}).(Correlation)
	requestID := correlation.RequestID
	if value := RequestIDFromContext(ctx); value != "" {
		requestID = value
	}
	addCorrelationAttr(&attrs, "request_id", requestID)
	addCorrelationAttr(&attrs, "finding_id", correlation.FindingID)
	addCorrelationAttr(&attrs, "incident_id", correlation.IncidentID)
	addCorrelationAttr(&attrs, "investigation_job_id", correlation.InvestigationJobID)
	addCorrelationAttr(&attrs, "action_plan_id", correlation.ActionPlanID)
	addCorrelationAttr(&attrs, "execution_id", correlation.ExecutionID)
	spanContext := trace.SpanFromContext(ctx).SpanContext()
	if spanContext.IsValid() {
		attrs = append(attrs, "trace_id", spanContext.TraceID().String(), "span_id", spanContext.SpanID().String())
	}
	if len(attrs) == 0 {
		return logger
	}
	return logger.With(attrs...)
}

func addCorrelationAttr(attrs *[]any, key, value string) {
	if value == "" || len(value) > 128 {
		return
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:", r)) {
			return
		}
	}
	*attrs = append(*attrs, key, value)
}

func NewRequestID() string { return uuid.NewString() }

func validRequestID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}

func ValidRequestID(value string) bool { return validRequestID(value) }

type redactingHandler struct {
	next   slog.Handler
	groups []string
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, scrubText(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		clean.AddAttrs(redactAttr(attr, h.groups))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		clean = append(clean, redactAttr(attr, h.groups))
	}
	return &redactingHandler{next: h.next.WithAttrs(clean), groups: append([]string(nil), h.groups...)}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	groups := append([]string(nil), h.groups...)
	groups = append(groups, name)
	return &redactingHandler{next: h.next.WithGroup(name), groups: groups}
}

func redactAttr(attr slog.Attr, groups []string) slog.Attr {
	keyParts := append(append([]string(nil), groups...), attr.Key)
	key := strings.Join(keyParts, ".")
	if sensitiveKey(key) {
		return slog.String(attr.Key, RedactedValue)
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		children := value.Group()
		clean := make([]slog.Attr, 0, len(children))
		for _, child := range children {
			clean = append(clean, redactAttr(child, keyParts))
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(clean...)}
	}
	if value.Kind() == slog.KindString {
		return slog.String(attr.Key, scrubText(value.String()))
	}
	if value.Kind() == slog.KindAny {
		if err, ok := value.Any().(error); ok {
			return slog.String(attr.Key, scrubText(err.Error()))
		}
		if value.Any() == nil {
			return slog.String(attr.Key, "")
		}
		return slog.String(attr.Key, scrubText(fmt.Sprint(value.Any())))
	}
	return slog.Attr{Key: attr.Key, Value: value}
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	for _, fragment := range []string{"token", "password", "secret", "credential", "privatekey", "apikey", "clientsecret", "recovery", "command", "prompt", "authorization", "cookie", "databaseurl", "dsn"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func scrubText(value string) string {
	value = privateKeyPattern.ReplaceAllString(value, RedactedValue)
	value = urlCredentialPattern.ReplaceAllString(value, "$1://[REDACTED]@")
	value = bearerPattern.ReplaceAllString(value, "$1 "+RedactedValue)
	value = jwtPattern.ReplaceAllString(value, RedactedValue)
	value = credentialPattern.ReplaceAllString(value, "$1="+RedactedValue)
	return value
}
