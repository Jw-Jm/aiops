package supplychain

import "strings"

// The catalog accepts reviewed SPDX expressions, not arbitrary scanner labels.
// OR retains the publisher's choice; AND retains all independently licensed
// files. An exception is accepted only for its explicitly reviewed pairing.
var licenseExceptions = map[string]map[string]bool{
	"Classpath-exception-2.0":      {"GPL-2.0-only": true, "GPL-2.0-or-later": true},
	"GCC-exception-2.0":            {"GPL-2.0-only": true, "GPL-2.0-or-later": true, "LGPL-2.1-or-later": true},
	"GCC-exception-3.1":            {"GPL-3.0-only": true, "GPL-3.0-or-later": true},
	"LLVM-exception":               {"Apache-2.0": true},
	"Linux-syscall-note":           {"GPL-2.0-only": true},
	"Universal-FOSS-exception-1.0": {"GPL-2.0-only": true},
}

type licenseExpression struct {
	tokens []string
	index  int
}

func validLicenseExpression(value string) bool {
	if len(value) == 0 || len(value) > 4096 {
		return false
	}
	tokens := strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(value))
	if len(tokens) > 256 {
		return false
	}
	p := licenseExpression{tokens: tokens}
	return p.expression(0) && p.index == len(p.tokens)
}

func (p *licenseExpression) expression(depth int) bool {
	if depth > 32 || !p.atom(depth) {
		return false
	}
	for p.index < len(p.tokens) && (p.tokens[p.index] == "AND" || p.tokens[p.index] == "OR") {
		p.index++
		if !p.atom(depth) {
			return false
		}
	}
	return true
}

func (p *licenseExpression) atom(depth int) bool {
	if p.index >= len(p.tokens) {
		return false
	}
	token := p.tokens[p.index]
	p.index++
	if token == "(" {
		if !p.expression(depth+1) || p.index >= len(p.tokens) || p.tokens[p.index] != ")" {
			return false
		}
		p.index++
		return true
	}
	if _, known := knownLicenses[token]; !known {
		if _, reviewed := nativeLicenses[token]; !reviewed {
			return false
		}
	}
	if p.index < len(p.tokens) && p.tokens[p.index] == "WITH" {
		p.index++
		if p.index >= len(p.tokens) || !licenseExceptions[p.tokens[p.index]][token] {
			return false
		}
		p.index++
	}
	return true
}

func licenseContains(value, prefix string) bool {
	for _, token := range strings.Fields(strings.NewReplacer("(", " ", ")", " ").Replace(value)) {
		if strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}
