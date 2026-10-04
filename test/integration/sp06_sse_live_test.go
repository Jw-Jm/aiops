package integration

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"ops-platform/internal/auth"
	"ops-platform/internal/httpapi"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSP06DurableSSETerminalBatchesCursorAndWithdrawal(t *testing.T) {
	ctx, db, repo, req := sp06JobFixture(t)
	job, err := repo.CreateJob(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	l, err := repo.Claim(ctx, job.TenantID, "sse-worker", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 90 {
		if err = repo.RecordDenial(ctx, l, "get_findings", "CURRENT_POLICY_DENIED"); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Stop(ctx, job.TenantID, job.JobID, job.Subject, false); err != nil {
		t.Fatal(err)
	}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	handler := &httpapi.InvestigationHandlers{Repository: repo, CursorKey: key}
	actor := auth.RequestContext{TenantID: job.TenantID, Subject: job.Subject, Roles: []auth.Role{auth.Operator}, TokenExpiresAt: time.Now().Add(time.Minute)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r.WithContext(auth.WithRequestContext(r.Context(), actor)))
	}))
	defer server.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	path := server.URL + "/api/v1/investigations/" + job.JobID.String() + "/events"
	response, err := client.Get(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("terminal SSE status=%d %v", response.StatusCode, err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(body)))
	last := int64(0)
	cursor := ""
	firstCursor := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			cursor = strings.TrimPrefix(line, "id: ")
			if firstCursor == "" {
				firstCursor = cursor
			}
		}
		if strings.HasPrefix(line, "data: ") {
			var event struct {
				EventID string `json:"eventId"`
			}
			if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
				t.Fatal("SSE JSON")
			}
			var sequence int64
			json.Unmarshal([]byte(event.EventID), &sequence)
			if sequence != last+1 {
				t.Fatalf("event gap %d -> %d", last, sequence)
			}
			last = sequence
		}
	}
	final, err := repo.Get(ctx, job.TenantID, job.JobID)
	if err != nil || last != final.EventSeq || last < 90 {
		t.Fatalf("terminal stream lost persisted batches: got=%d durable=%d err=%v", last, final.EventSeq, err)
	}
	read := func(c string) (int, string) {
		r, _ := http.NewRequestWithContext(context.Background(), "GET", path, nil)
		r.Header.Set("Last-Event-ID", c)
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		b, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(b)
	}
	if status, b := read(cursor); status != 200 || b != "" {
		t.Fatalf("terminal cursor replay status=%d body=%q", status, b)
	}
	if status, b := read(firstCursor); status != 200 || strings.Count(b, "data: ") != int(last-1) {
		t.Fatalf("restart resume status=%d count=%d", status, strings.Count(b, "data: "))
	}
	parts := strings.Split(cursor, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var claims map[string]any
	json.Unmarshal(raw, &claims)
	claims["exp"] = time.Now().Add(-time.Second).Unix()
	raw, _ = json.Marshal(claims)
	expired := base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))
	if status, _ := read(expired); status != 410 {
		t.Fatalf("expired durable cursor status=%d", status)
	}
	if ed25519.Verify(pub, raw, ed25519.Sign(key, raw)) != true {
		t.Fatal("cursor signature")
	}
	if _, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, job.TenantID, job.Subject); err != nil {
		t.Fatal(err)
	}
	if status, _ := read(cursor); status != 403 {
		t.Fatalf("role-withdrawn persisted stream status=%d", status)
	}
	t.Log("durable terminal SSE drains >64 events, monotonic IDs, duplicate-free resume, signed expired cursor 410 and current role withdrawal 403")
}

func TestSP06ActiveSSEClosesOnCurrentRoleOrSourceWithdrawal(t *testing.T) {
	for _, target := range []string{"role", "source"} {
		t.Run(target, func(t *testing.T) {
			ctx, db, repo, request := sp06JobFixture(t)
			job, err := repo.CreateJob(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.Claim(ctx, job.TenantID, "active-stream", time.Minute); err != nil {
				t.Fatal(err)
			}
			_, key, _ := ed25519.GenerateKey(rand.Reader)
			handler := &httpapi.InvestigationHandlers{Repository: repo, CursorKey: key}
			actor := auth.RequestContext{TenantID: job.TenantID, Subject: job.Subject, Roles: []auth.Role{auth.Operator}, TokenExpiresAt: time.Now().Add(time.Minute)}
			ended := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handler.ServeHTTP(w, r.WithContext(auth.WithRequestContext(r.Context(), actor)))
				ended <- struct{}{}
			}))
			defer server.Close()
			response, err := http.Get(server.URL + "/api/v1/investigations/" + job.JobID.String() + "/events")
			if err != nil || response.StatusCode != 200 {
				t.Fatalf("active stream %v", err)
			}
			defer response.Body.Close()
			reader := bufio.NewReader(response.Body)
			seen := 0
			for seen < 2 {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(line, "data: ") {
					seen++
				}
			}
			if target == "role" {
				_, err = db.ExecContext(ctx, `DELETE FROM platform.role_bindings WHERE tenant_id=$1 AND subject=$2`, job.TenantID, job.Subject)
			} else {
				_, err = db.ExecContext(ctx, `UPDATE platform.source_registrations SET status='disabled',revision=revision+1 WHERE tenant_id=$1`, job.TenantID)
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-ended:
			case <-time.After(3 * time.Second):
				t.Fatal("active SSE retained withdrawn current authority")
			}
			remaining, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(remaining), "data: ") {
				t.Fatalf("new events emitted after withdrawal: %s", remaining)
			}
			t.Log("active durable stream terminates on current " + target + " withdrawal without cached authority")
		})
	}
}

type sp06CountingSSEWriter struct {
	http.ResponseWriter
	count int
}

func (w *sp06CountingSSEWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *sp06CountingSSEWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if err == nil && strings.Contains(string(b), "data: ") {
		w.count++
	}
	return n, err
}

// A real HTTP connection over net.Pipe gives deterministic transport
// backpressure when the client stops reading, independent of OS TCP buffering.
// Ordinary TCP SSE and reconnects are exercised in the other live cases.
type sp06PipeListener struct {
	conn     net.Conn
	accepted bool
	closed   chan struct{}
	once     sync.Once
}

func (l *sp06PipeListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}
	<-l.closed
	return nil, net.ErrClosed
}
func (l *sp06PipeListener) Close() error {
	l.once.Do(func() { close(l.closed); _ = l.conn.Close() })
	return nil
}
func (l *sp06PipeListener) Addr() net.Addr { return l.conn.LocalAddr() }

// This checks a transport deadline and recovery, without recording throughput,
// latency percentiles or capacity.
func TestSP06SlowSSEClientWriteDeadlineAndDurableRecovery(t *testing.T) {
	ctx, _, repo, request := sp06JobFixture(t)
	job, err := repo.CreateJob(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repo.Claim(ctx, job.TenantID, "slow-stream", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for range 90 {
		if err = repo.RecordDenial(ctx, lease, "get_findings", strings.Repeat("D", 128)); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Stop(ctx, job.TenantID, job.JobID, job.Subject, false); err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	handler := &httpapi.InvestigationHandlers{Repository: repo, CursorKey: key}
	actor := auth.RequestContext{TenantID: job.TenantID, Subject: job.Subject, Roles: []auth.Role{auth.Operator}, TokenExpiresAt: time.Now().Add(time.Minute)}
	ended := make(chan struct{}, 1)
	firstCount := make(chan int, 1)
	serve := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tracked := &sp06CountingSSEWriter{ResponseWriter: w}
		handler.ServeHTTP(tracked, r.WithContext(auth.WithRequestContext(r.Context(), actor)))
		select {
		case firstCount <- tracked.count:
		default:
		}
		select {
		case ended <- struct{}{}:
		default:
		}
	})
	serverConn, clientConn := net.Pipe()
	listener := &sp06PipeListener{conn: serverConn, closed: make(chan struct{})}
	server := &http.Server{Handler: serve}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	defer clientConn.Close()
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return clientConn, nil }}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	pathSuffix := "/api/v1/investigations/" + job.JobID.String() + "/events"
	path := "http://sse.test" + pathSuffix
	response, err := client.Get(path)
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("slow subscription: %v", err)
	}
	// Read no body while the real HTTP server flushes into a blocked pipe.
	select {
	case <-ended:
	case <-time.After(6 * time.Second):
		response.Body.Close()
		t.Fatal("slow client exceeded configured write deadline")
	}
	response.Body.Close()
	if written := <-firstCount; written >= 93 {
		t.Fatalf("slow fixture did not exercise a blocked write: %d events", written)
	}
	recoveryServer := httptest.NewServer(serve)
	defer recoveryServer.Close()
	response, err = http.Get(recoveryServer.URL + pathSuffix)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 {
		t.Fatalf("durable retry %v", err)
	}
	final, err := repo.Get(ctx, job.TenantID, job.JobID)
	if err != nil || strings.Count(string(body), "data: ") != int(final.EventSeq) {
		t.Fatal("slow reader changed or lost durable events")
	}
	t.Log("backpressured HTTP pipe terminates at bounded write deadline; fresh TCP client reconstructs every persisted event")
}
