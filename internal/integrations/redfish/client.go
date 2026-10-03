package redfish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/stmcginnis/gofish"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	Endpoint, Tenant, Scope, SourceID, Username, Password string
	Client                                                *http.Client
	Diagnostics                                           bool
	Clock                                                 func() time.Time
	alarms                                                map[string]bool
}

func (c Config) observationTime() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

type readOnlyTransport struct {
	base               http.RoundTripper
	origin             *url.URL
	username, password string
	alarms             map[string]bool
}

func (t readOnlyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet || r.URL.Scheme != t.origin.Scheme || r.URL.Host != t.origin.Host || r.URL.User != nil || !strings.HasPrefix(r.URL.Path, "/redfish/v1/") {
		return nil, errors.New("Redfish read scope rejected")
	}
	// Gofish fetches ServiceRoot before installing its BasicAuth state. Some
	// BMCs require authentication even for that first read. Bind credentials
	// only after the exact origin/method/path guard has accepted the request.
	copy := r.Clone(r.Context())
	copy.Header = r.Header.Clone()
	if t.username != "" {
		copy.SetBasicAuth(t.username, t.password)
	}
	res, err := t.base.RoundTrip(copy)
	if err == nil && res.Body != nil {
		res.Body = &boundedBody{Reader: io.LimitReader(res.Body, (1<<20)+1), Closer: res.Body}
		if t.alarms != nil {
			body, readErr := io.ReadAll(res.Body)
			res.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			if len(body) > 1<<20 {
				return nil, errors.New("Redfish response exceeds byte budget")
			}
			res.Body = io.NopCloser(bytes.NewReader(body))
			// Gofish's bool value cannot distinguish absent from false. Preserve
			// just field presence at the native transport boundary; Gofish remains
			// the typed collection/metrics parser. No raw source body is retained.
			var presence struct {
				HealthData struct {
					AlarmTrips struct{ UncorrectableECCError *bool }
				}
			}
			if json.Unmarshal(body, &presence) == nil && presence.HealthData.AlarmTrips.UncorrectableECCError != nil {
				t.alarms[r.URL.Path] = *presence.HealthData.AlarmTrips.UncorrectableECCError
			}
		}
	}
	return res, err
}

type boundedBody struct {
	io.Reader
	io.Closer
}

func connect(ctx context.Context, c Config) (*gofish.APIClient, context.CancelFunc, error) {
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || c.Client == nil {
		return nil, func() {}, errors.New("invalid Redfish endpoint")
	}

	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return nil, func() {}, errors.New("Redfish credentials require HTTPS outside loopback fixtures")
		}
	}
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	client := *c.Client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	client.Transport = readOnlyTransport{base: base, origin: u, username: c.Username, password: c.Password, alarms: c.alarms}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	fish, err := gofish.ConnectContext(deadline, gofish.ClientConfig{Endpoint: strings.TrimSuffix(c.Endpoint, "/"), Username: c.Username, Password: c.Password, BasicAuth: true, HTTPClient: &client, NoModifyTransport: true, MaxConcurrentRequests: 1})
	return fish, cancel, err
}
