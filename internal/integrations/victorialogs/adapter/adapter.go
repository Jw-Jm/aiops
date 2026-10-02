package adapter

import (
	"net/http"
	"ops-platform/internal/evidence"
)

func New(b evidence.Binding, c *http.Client, a evidence.Authorizer) (evidence.Adapter, error) {
	return evidence.NewVictoria("victorialogs", b, c, a)
}
