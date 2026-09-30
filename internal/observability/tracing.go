package observability

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
)

type TracingConfig struct {
	ServiceName string
	Endpoint    string
	Enabled     bool
	SampleRatio float64
}

type Tracing struct {
	provider   trace.TracerProvider
	sdk        *sdktrace.TracerProvider
	propagator propagation.TextMapPropagator
}

func TracingConfigFromEnv(serviceName string) TracingConfig {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	}
	enabled := endpoint != ""
	if raw := strings.TrimSpace(os.Getenv("PLATFORM_TRACING_ENABLED")); raw != "" {
		if value, err := strconv.ParseBool(raw); err == nil {
			enabled = value
			if !value {
				endpoint = ""
			}
		} else {
			enabled = false
			endpoint = ""
		}
	}
	return TracingConfig{ServiceName: serviceName, Endpoint: endpoint, Enabled: enabled, SampleRatio: 0.1}
}

// NewTracing treats the exporter as an optional side channel: invalid or
// unavailable telemetry configuration degrades tracing but does not prevent the
// API or worker from starting. An empty endpoint keeps W3C propagation available
// while disabling trace export.
func NewTracing(ctx context.Context, config TracingConfig, metrics *Metrics, logger *slog.Logger) (*Tracing, error) {
	if metrics == nil {
		metrics = NewMetrics()
	}
	if config.ServiceName == "" {
		config.ServiceName = "ops-platform"
	}
	propagator := propagation.TraceContext{}
	if logger == nil {
		logger = NewLogger(nil, slog.LevelInfo)
	}
	tracing := &Tracing{provider: trace.NewNoopTracerProvider(), propagator: propagator}
	enabled := config.Enabled || strings.TrimSpace(config.Endpoint) != ""
	if !enabled || strings.TrimSpace(config.Endpoint) == "" {
		return tracing, nil
	}
	endpoint, err := validatedTraceEndpoint(config.Endpoint)
	if err != nil {
		metrics.SetComponentDegraded("traces", true)
		logger.WarnContext(ctx, "trace exporter disabled because endpoint configuration is invalid", "signal", "traces")
		return tracing, nil
	}
	exporterOptions := []otlptracehttp.Option{
		otlptracehttp.WithEndpointURL(endpoint.String()),
		otlptracehttp.WithTimeout(2 * time.Second),
		otlptracehttp.WithHTTPClient(&http.Client{Transport: traceObservingTransport{base: http.DefaultTransport, metrics: metrics}}),
	}
	if endpoint.Scheme == "http" {
		exporterOptions = append(exporterOptions, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, exporterOptions...)
	if err != nil {
		metrics.SetComponentDegraded("traces", true)
		logger.WarnContext(ctx, "trace exporter could not be configured", "signal", "traces")
		return tracing, nil
	}
	ratio := config.SampleRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 0.1
	}
	resourceValue, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", config.ServiceName)))
	if err != nil {
		_ = exporter.Shutdown(ctx)
		metrics.SetComponentDegraded("traces", true)
		logger.WarnContext(ctx, "trace resource could not be configured", "signal", "traces")
		return tracing, nil
	}
	tracked := &trackingExporter{next: exporter, metrics: metrics, logger: logger}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))),
		sdktrace.WithResource(resourceValue),
		sdktrace.WithBatcher(tracked, sdktrace.WithBatchTimeout(time.Second)),
	)
	tracing.provider = provider
	tracing.sdk = provider
	return tracing, nil
}

func (t *Tracing) Tracer(name string) trace.Tracer { return t.provider.Tracer(name) }

func (t *Tracing) Extract(ctx context.Context, headers http.Header) context.Context {
	return t.propagator.Extract(ctx, propagation.HeaderCarrier(headers))
}

func (t *Tracing) Inject(ctx context.Context, headers http.Header) {
	t.propagator.Inject(ctx, propagation.HeaderCarrier(headers))
}

func (t *Tracing) Flush(ctx context.Context) error {
	if t.sdk == nil {
		return nil
	}
	return t.sdk.ForceFlush(ctx)
}

func (t *Tracing) Close(ctx context.Context) error {
	if t.sdk == nil {
		return nil
	}
	return t.sdk.Shutdown(ctx)
}

func validatedTraceEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.ParseRequestURI(raw)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, errors.New("trace exporter endpoint must be an http(s) URL without credentials, query, or fragment")
	}
	return endpoint, nil
}

type trackingExporter struct {
	next    sdktrace.SpanExporter
	metrics *Metrics
	logger  *slog.Logger
}

type traceObservingTransport struct {
	base    http.RoundTripper
	metrics *Metrics
}

func (t traceObservingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		t.metrics.ObserveExport("traces", err)
		return response, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		t.metrics.ObserveExport("traces", errors.New("trace collector returned a failure status"))
	} else {
		t.metrics.ObserveExport("traces", nil)
	}
	return response, nil
}

func (e *trackingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	err := e.next.ExportSpans(ctx, spans)
	e.metrics.ObserveExport("traces", err)
	if err != nil {
		e.logger.WarnContext(ctx, "trace export failed", "signal", "traces")
	}
	return err
}

func (e *trackingExporter) Shutdown(ctx context.Context) error {
	err := e.next.Shutdown(ctx)
	if err != nil {
		e.metrics.ObserveExport("traces", err)
	}
	return err
}
