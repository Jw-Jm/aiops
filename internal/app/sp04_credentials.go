package app

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// The Kubernetes collection credential is a short-lived projected ServiceAccount
// token. The kubelet rewrites the projected file before expiry, so the client
// must read it on every request instead of caching it: a cached token would
// expire and collection would stop until an operator intervened.
type clusterCredentialTransport struct {
	base http.RoundTripper
	file string
}

func (t clusterCredentialTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	token, err := readProjectedToken(t.file)
	if err != nil {
		return nil, err
	}
	copy := request.Clone(request.Context())
	copy.Header = request.Header.Clone()
	copy.Header.Set("Authorization", "Bearer "+token)
	return t.base.RoundTrip(copy)
}

// readProjectedToken accepts only a single non-empty JWT. Any other content is
// rejected before a request is made, so a truncated, rotated-away or replaced
// credential fails closed instead of being sent as an empty bearer value.
func readProjectedToken(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("cluster collection credential unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil || len(raw) == 0 || len(raw) > 64<<10 {
		return "", errors.New("cluster collection credential invalid")
	}
	token := strings.TrimSpace(string(raw))
	if token == "" || strings.ContainsAny(token, " \t\r\n") || len(token) < 32 || strings.Count(token, ".") != 2 {
		return "", errors.New("cluster collection credential invalid")
	}
	return token, nil
}

// projectionMetadata returns only non-secret token timing and a digest prefix.
// The signature and the token itself are never logged, stored or compared
// outside this process.
type projectionMetadata struct {
	DigestPrefix string
	IssuedAt     time.Time
	ExpiresAt    time.Time
	ModTime      time.Time
}

func observeProjection(path string) (projectionMetadata, error) {
	token, err := readProjectedToken(path)
	if err != nil {
		return projectionMetadata{}, err
	}
	stat, err := os.Stat(path)
	if err != nil {
		return projectionMetadata{}, err
	}
	digest := sha256.Sum256([]byte(token))
	out := projectionMetadata{DigestPrefix: hex.EncodeToString(digest[:4]), ModTime: stat.ModTime().UTC()}
	parts := strings.Split(token, ".")
	if payload, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
		var claims struct {
			IssuedAt  int64 `json:"iat"`
			ExpiresAt int64 `json:"exp"`
		}
		if json.Unmarshal(payload, &claims) == nil {
			if claims.IssuedAt > 0 {
				out.IssuedAt = time.Unix(claims.IssuedAt, 0).UTC()
			}
			if claims.ExpiresAt > 0 {
				out.ExpiresAt = time.Unix(claims.ExpiresAt, 0).UTC()
			}
		}
	}
	return out, nil
}

// watchClusterCredential records automatic rotation as bounded evidence: when
// the projected file changed, when the new token is valid from/to, and a digest
// prefix that identifies it without exposing it.
func watchClusterCredential(ctx context.Context, logger *slog.Logger, cluster SP04Cluster) {
	previous := projectionMetadata{}
	previousErr := ""
	check := func() {
		metadata, err := observeProjection(cluster.TokenFile)
		if err != nil {
			if previousErr != err.Error() {
				previousErr = err.Error()
				if logger != nil {
					logger.WarnContext(ctx, "Cluster collection credential unavailable", slog.String("clusterUid", cluster.ClusterUID), slog.String("reason", err.Error()))
				}
			}
			return
		}
		previousErr = ""
		if metadata.DigestPrefix == previous.DigestPrefix && metadata.ModTime.Equal(previous.ModTime) {
			return
		}
		previous = metadata
		if logger != nil {
			logger.InfoContext(ctx, "Cluster collection credential rotated", slog.String("clusterUid", cluster.ClusterUID), slog.String("digestPrefix", metadata.DigestPrefix), slog.Time("issuedAt", metadata.IssuedAt), slog.Time("expiresAt", metadata.ExpiresAt), slog.Time("fileModifiedAt", metadata.ModTime))
		}
	}
	check()
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
