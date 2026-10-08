package bootstrap

import (
	"strings"
	"testing"
)

func TestBootstrapSCRAMInputsRejectUnsupportedNormalization(t *testing.T) {
	c := DatabaseLogins{SchemaVersion: 1, Migration: DatabaseLogin{"ops_migrator", "private-ascii-test-password-12345"}, API: DatabaseLogin{"ops_api", "private-ascii-test-password-23456"}, Worker: DatabaseLogin{"ops_worker", "private-ascii-test-password-34567"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{strings.Repeat("é", 24), "private-ascii-test-password\x00", strings.Repeat(" ", 24)} {
		c.Worker.Password = password
		if c.Validate() == nil {
			t.Fatal("unsupported password normalization or control input accepted")
		}
	}
}
