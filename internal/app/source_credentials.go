package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Source credentials are refreshed from their Secret projection on each call.
// Existing external bearer files retain their wire semantics; the explicit
// versioned Basic form supports the locked Victoria native HTTP servers. It is
// deliberately separate from Kubernetes projected ServiceAccount JWT handling.
type httpSourceTransport struct {
	base http.RoundTripper
	file string
}

func (t httpSourceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	f, err := os.Open(t.file)
	if err != nil {
		return nil, errors.New("source credential unavailable")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	f.Close()
	if err != nil || len(raw) == 0 || len(raw) > 64<<10 {
		return nil, errors.New("source credential invalid")
	}
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		var credential struct {
			SchemaVersion string `json:"schemaVersion"`
			Mode          string `json:"mode"`
			Username      string `json:"username"`
			Password      string `json:"password"`
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&credential) != nil || decoder.Decode(new(any)) != io.EOF || credential.SchemaVersion != "ops-source-http-credentials/v1" || credential.Mode != "basic" || !regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`).MatchString(credential.Username) || !regexp.MustCompile(`^[!-~]{24,256}$`).MatchString(credential.Password) || request.URL.Scheme != "https" {
			return nil, errors.New("source Basic credential requires its explicit version and TLS")
		}
		copy.SetBasicAuth(credential.Username, credential.Password)
	} else {
		token := strings.TrimSpace(string(raw))
		if token == "" || strings.ContainsAny(token, " \t\r\n") {
			return nil, errors.New("source bearer credential invalid")
		}
		copy.Header.Set("Authorization", "Bearer "+token)
	}
	return t.base.RoundTrip(copy)
}
