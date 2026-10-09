package openbao

import (
	"context"
)

// ProjectedTransit shares the existing serialized, fail-closed authentication
// lifecycle while keeping a separate client from signing/PKI operations.
type ProjectedTransit struct{ s *ProjectedInvocationSigning }

func NewProjectedTransit(c *Client, path, sa string) (*ProjectedTransit, error) {
	s, err := NewProjectedInvocationSigning(c, path, sa)
	if err != nil {
		return nil, err
	}
	return &ProjectedTransit{s}, nil
}
func (p *ProjectedTransit) TransitEncrypt(ctx context.Context, key string, b, aad []byte) (TransitCiphertext, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if err := p.s.authenticate(ctx); err != nil {
		return TransitCiphertext{}, err
	}
	return p.s.client.TransitEncrypt(ctx, key, b, aad)
}
func (p *ProjectedTransit) TransitDecrypt(ctx context.Context, key, value string, aad []byte) ([]byte, error) {
	p.s.mu.Lock()
	defer p.s.mu.Unlock()
	if err := p.s.authenticate(ctx); err != nil {
		return nil, err
	}
	return p.s.client.TransitDecrypt(ctx, key, value, aad)
}
