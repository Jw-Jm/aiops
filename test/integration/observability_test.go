package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"ops-platform/internal/observability"
)

func TestRealVictoriaMetricsScrapesPlatformProcessMetric(t *testing.T) {
	endpoint := strings.TrimRight(os.Getenv("SP03_TEST_VICTORIA_METRICS_URL"), "/")
	if endpoint == "" {
		t.Skip("isolated VictoriaMetrics endpoint is required")
	}
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		values := url.Values{"query": {`platform_process_up{job="ops-platform-sp03"}`}}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"/api/v1/query?"+values.Encode(), nil)
		if err != nil {
			t.Fatal("build isolated VictoriaMetrics query")
		}
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				var result struct {
					Status string `json:"status"`
					Data   struct {
						Result []struct {
							Value []any `json:"value"`
						} `json:"result"`
					} `json:"data"`
				}
				if json.Unmarshal(body, &result) == nil && result.Status == "success" && len(result.Data.Result) > 0 && len(result.Data.Result[0].Value) == 2 && result.Data.Result[0].Value[1] == "1" {
					return
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("isolated VictoriaMetrics did not scrape platform_process_up=1")
}

func TestRealVictoriaLogsAcceptsStructuredPlatformStdout(t *testing.T) {
	endpoint := strings.TrimRight(os.Getenv("SP03_TEST_VICTORIA_LOGS_URL"), "/")
	if endpoint == "" {
		t.Skip("isolated VictoriaLogs endpoint is required")
	}
	requestID := "sp03-" + uuid.NewString()
	var output bytes.Buffer
	logger := observability.NewLogger(&output, slog.LevelInfo).With("service", "ops-platform-api")
	logger.InfoContext(context.Background(), "platform observability integration event", "request_id", requestID, "authorization", "Bearer must-not-appear")
	if strings.Contains(output.String(), "must-not-appear") {
		t.Fatal("structured platform stdout contained the test credential")
	}
	insertURL := endpoint + "/insert/jsonline?_stream_fields=service&_time_field=time&_msg_field=msg"
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, insertURL, bytes.NewReader(output.Bytes()))
	if err != nil {
		t.Fatal("build VictoriaLogs insert request")
	}
	request.Header.Set("Content-Type", "application/stream+json")
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send sanitized platform stdout to isolated VictoriaLogs: %v", err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if readErr != nil || response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("VictoriaLogs rejected platform stdout: status=%d", response.StatusCode)
	}
	query := url.Values{"query": {"request_id:" + requestID}}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		queryRequest, err := http.NewRequestWithContext(t.Context(), http.MethodGet, endpoint+"/select/logsql/query?"+query.Encode(), nil)
		if err != nil {
			t.Fatal("build VictoriaLogs query")
		}
		queryResponse, err := client.Do(queryRequest)
		if err == nil {
			queryBody, queryReadErr := io.ReadAll(io.LimitReader(queryResponse.Body, 1<<20))
			_ = queryResponse.Body.Close()
			if queryReadErr == nil && queryResponse.StatusCode == http.StatusOK && bytes.Contains(queryBody, []byte(requestID)) && bytes.Contains(queryBody, []byte("platform observability integration event")) {
				if bytes.Contains(queryBody, []byte("must-not-appear")) {
					t.Fatal("VictoriaLogs returned a credential that should have been redacted")
				}
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("isolated VictoriaLogs did not return ingested structured stdout: %s", strings.TrimSpace(string(body)))
}
