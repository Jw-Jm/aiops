package supplychain

import "testing"

func TestLicenseExpressionRetainsChoicesAndRejectsUnknownTerms(t *testing.T) {
	for _, value := range []string{"MIT", "MPL-2.0 AND MIT", "(GPL-2.0-only OR BSD-3-Clause) AND MIT", "GPL-2.0-only WITH Classpath-exception-2.0"} {
		if !validLicenseExpression(value) {
			t.Errorf("reject valid expression %q", value)
		}
	}
	for _, value := range []string{"", "MIT OR", "MIT AND Unknown", "MIT MIT", "(MIT", "MIT)", "MIT WITH Classpath-exception-2.0", "GPL-2.0-only WITH Unknown-exception", "LicenseRef-anything", "MIT or Apache-2.0", "(GPL-2.0-only) WITH Classpath-exception-2.0"} {
		if validLicenseExpression(value) {
			t.Errorf("accept malformed/unreviewed expression %q", value)
		}
	}
	if !requiresSpecialLicenseADR("MIT AND GPL-2.0-only") || !isAGPL("MIT OR AGPL-3.0-only") {
		t.Fatal("an embedded copyleft term must not evade admission gates")
	}
}
