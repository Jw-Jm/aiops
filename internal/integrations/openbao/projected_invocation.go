package openbao

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"sync"
	"time"
)

// ProjectedInvocationSigning owns a dedicated client. Token renewal and all
// Transit calls are serialized, so concurrent Context operations cannot race
// a token update. Authentication failures never fall back to an old token.
type ProjectedInvocationSigning struct {
	client    *Client
	tokenPath string
	role      string
	renewAt   time.Time
	mu        sync.Mutex
	now       func() time.Time
}

func NewProjectedInvocationSigning(client *Client, tokenPath, serviceAccount string) (*ProjectedInvocationSigning, error) {
	role, err := WorkloadAuthRoleName(serviceAccount)
	if err != nil || client == nil || tokenPath == "" {
		return nil, errors.New("projected invocation signer requires explicit workload credentials")
	}
	return &ProjectedInvocationSigning{client: client, tokenPath: tokenPath, role: role, now: time.Now}, nil
}

func (s *ProjectedInvocationSigning) authenticate(ctx context.Context) error {
	if s.now().Before(s.renewAt) {
		return nil
	}
	ttl, err := s.client.LoginProjectedServiceAccount(ctx, s.tokenPath, s.role)
	if err != nil || ttl <= 0 || ttl > time.Hour {
		return errors.New("INVOCATION_WORKLOAD_AUTHENTICATION_UNAVAILABLE")
	}
	s.renewAt = s.now().Add(ttl * 2 / 3)
	return nil
}

func (s *ProjectedInvocationSigning) SigningKeys(ctx context.Context, key string) (map[int]*ecdsa.PublicKey, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authenticate(ctx); err != nil {
		return nil, 0, err
	}
	return s.client.SigningKeys(ctx, key)
}

func (s *ProjectedInvocationSigning) SignJWS(ctx context.Context, key string, version int, message []byte) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.authenticate(ctx); err != nil {
		return nil, err
	}
	return s.client.SignJWS(ctx, key, version, message)
}
