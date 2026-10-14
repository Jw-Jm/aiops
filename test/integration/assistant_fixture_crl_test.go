package integration

import (
	"context"
	"os"
	"time"
)

// These isolated signed CRLs have no issuing service. Model their periodic
// fetch while long browser acceptance runs. Signature, expiry and revocation
// checks remain active; this is not production PKI refresh evidence.
func refreshAssistantFixtureCRL(ctx context.Context, path string) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now()
				_ = os.Chtimes(path, now, now)
			}
		}
	}()
}
