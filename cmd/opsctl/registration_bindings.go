package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"ops-platform/internal/auth"
	"ops-platform/internal/bundle"
	"ops-platform/internal/profile"
)

func verifyInstalledSourceBindings(ctx context.Context, p profile.ResolvedProfile, b bundle.BusinessValues, tokenPath string) (bundle.BusinessValues, error) {
	info, err := os.Stat(tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !outsideGitTree(tokenPath) {
		return b, errors.New("registration credential requires a private repository-external regular file")
	}
	raw, err := readBoundedFile(tokenPath, 64<<10)
	if err != nil {
		return b, errors.New("private registration credential unavailable")
	}
	headers, err := registrationAuthHeaders(raw, b.NativeIdentity())
	if err != nil {
		return b, err
	}
	address, stop, err := serviceBootstrapForward(ctx, p.Kubernetes.Context, b.Namespace(), "ops-api", 8080)
	if err != nil {
		return b, err
	}
	defer stop()
	// The tunnel is authenticated by the selected Kubernetes context. HTTP is
	// confined to its loopback endpoint and the installed API Service; there is
	// no caller-supplied URL, redirect, proxy or general-purpose fetch facility.
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var registrations []bundle.RegisteredSource
	for _, tenant := range b.Tenants() {
		cursor := ""
		seen := map[string]bool{}
		for page := 0; ; page++ {
			if page >= 10 {
				return b, errors.New("source registration verification exceeds bounded page count")
			}
			query := url.Values{"limit": {"200"}}
			if cursor != "" {
				query.Set("cursor", cursor)
			}
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/v1/admin/source-registrations?"+query.Encode(), nil)
			if err != nil {
				return b, errors.New("fixed registration request unavailable")
			}
			req.Header = headers.Clone()
			req.Header.Set("X-Tenant-ID", tenant)
			response, err := client.Do(req)
			if err != nil {
				return b, errors.New("installed source registration API unavailable")
			}
			payload, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
			response.Body.Close()
			if readErr != nil || len(payload) > 2<<20 || response.StatusCode != http.StatusOK {
				return b, errors.New("installed source registration API rejected request or exceeded response bound")
			}
			var envelope struct {
				Data []bundle.RegisteredSource `json:"data"`
				Meta struct {
					NextCursor *string `json:"nextCursor"`
				} `json:"meta"`
			}
			if json.Unmarshal(payload, &envelope) != nil {
				return b, errors.New("installed source registration response invalid")
			}
			for _, s := range envelope.Data {
				if s.TenantID.String() != tenant {
					return b, errors.New("registration response crossed tenant boundary")
				}
				registrations = append(registrations, s)
			}
			if envelope.Meta.NextCursor == nil || *envelope.Meta.NextCursor == "" {
				break
			}
			cursor = *envelope.Meta.NextCursor
			if seen[cursor] {
				return b, errors.New("registration response repeated a page cursor")
			}
			seen[cursor] = true
		}
	}
	return bundle.VerifyRegisteredBusinessSources(b, registrations)
}

func registrationAuthHeaders(raw []byte, native bool) (http.Header, error) {
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, "\r\n\t ") {
		return nil, errors.New("private registration credential is invalid")
	}
	headers := make(http.Header)
	if native {
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(token)
		if err != nil || len(decoded) != 32 {
			return nil, errors.New("native registration requires an opaque session token")
		}
		req := &http.Request{Header: headers}
		req.AddCookie(&http.Cookie{Name: auth.NativeSessionCookieName, Value: token})
	} else {
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers, nil
}
