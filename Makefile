GO ?= go
GO_SOURCES := $(shell find cmd internal gen test -type f -name '*.go' 2>/dev/null)

.PHONY: bootstrap check-toolchain generate check-generated check-runtime-source fmt lint test-unit test-contract test-integration test-replay test-e2e test-security bundle-dev-arm64 verify-bundle check

bootstrap:
	@if command -v mise >/dev/null 2>&1; then \
		mise install; \
	elif command -v asdf >/dev/null 2>&1; then \
		asdf install; \
	fi
	@$(MAKE) check-toolchain

check-toolchain:
	@set -eu; \
	check() { \
		name=$$1; expected=$$2; actual=$$3; \
		if [ "$$actual" != "$$expected" ]; then \
			echo "$$name version mismatch: expected $$expected, got $$actual" >&2; \
			exit 1; \
		fi; \
	}; \
	command -v $(GO) >/dev/null 2>&1 || { echo "go is required" >&2; exit 1; }; \
	command -v node >/dev/null 2>&1 || { echo "node is required" >&2; exit 1; }; \
	command -v pnpm >/dev/null 2>&1 || { echo "pnpm is required" >&2; exit 1; }; \
	command -v python3 >/dev/null 2>&1 || { echo "python3 is required" >&2; exit 1; }; \
	command -v uv >/dev/null 2>&1 || { echo "uv is required" >&2; exit 1; }; \
	check go 1.27.1 "$$($(GO) version | awk '{sub(/^go/, "", $$3); print $$3}')"; \
	check node v24.21.0 "$$(node --version)"; \
	check pnpm 12.7.0 "$$(pnpm --version)"; \
	check python3 3.12.14 "$$(python3 --version | awk '{print $$2}')"; \
	check uv 0.12.17 "$$(uv --version | awk '{print $$2}')"

generate:
	$(GO) generate ./...

check-generated:
	python3 scripts/check-generated.py "$(GO)" gen web/src/api/generated internal/persistence/dbgen services/investigator/src/investigator/output.schema.json

fmt:
	gofmt -w $(GO_SOURCES)

check-runtime-source:
	python3 scripts/prepare-runtime-source.py --check

lint:
	$(GO) vet ./...

test-unit:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test -timeout=30m ./... > artifacts/test-reports/unit.txt 2>&1; status=$$?; cat artifacts/test-reports/unit.txt; exit $$status
	@pnpm --dir web test

test-contract:
	@if [ -d test/contract ] && find test/contract -type f -name '*_test.go' -print -quit | grep -q .; then \
		mkdir -p artifacts/test-reports; \
		set +e; $(GO) test ./test/contract > artifacts/test-reports/contract.txt 2>&1; status=$$?; \
		cat artifacts/test-reports/contract.txt; exit $$status; \
	else \
		echo "No Contract tests are defined yet."; \
	fi

test-e2e:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/e2e -count=1 -json -skip '^TestKubeVirtPOCArtifactsAndOrbStackLifecycle$$' -timeout=20m > artifacts/test-reports/e2e.jsonl 2>&1; status=$$?; cat artifacts/test-reports/e2e.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/e2e.jsonl

test-integration:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/integration -count=1 -json -timeout=10m > artifacts/test-reports/integration.jsonl 2>&1; status=$$?; cat artifacts/test-reports/integration.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/integration.jsonl

test-security:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/security -count=1 -json > artifacts/test-reports/security.jsonl 2>&1; status=$$?; cat artifacts/test-reports/security.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/security.jsonl

test-replay:
	@mkdir -p artifacts/test-reports
	@set +e; OPS_GRAPH_REPLAY=1 OPS_INSPECTION_REPLAY=1 $(GO) test ./test/contract -run '^Test(Graph|Inspection)NonVirtualUpstreamReplay$$' -count=1 -json -timeout=25m > artifacts/test-reports/replay.jsonl 2>&1; status=$$?; cat artifacts/test-reports/replay.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/replay.jsonl

verify-bundle:
	@test -n "$(BUNDLE_DIR)" && test -n "$(TRUST_KEY)" || { echo "BUNDLE_DIR and TRUST_KEY are required" >&2; exit 1; }
	$(GO) run ./cmd/opsctl bundle verify --manifest "$(BUNDLE_DIR)/bundle.lock.json" --signature "$(BUNDLE_DIR)/bundle.lock.sig" --payload "$(BUNDLE_DIR)/payload.tar.zst" --key "$(TRUST_KEY)"

bundle-dev-arm64: check-toolchain check-runtime-source
	@test -n "$(BUNDLE_SPEC)" && test -n "$(BUNDLE_DIR)" && test -n "$(SIGNING_KEY)" || { echo "BUNDLE_SPEC, BUNDLE_DIR and external SIGNING_KEY are required; inputs must already be qualified" >&2; exit 1; }
	$(GO) run ./cmd/opsctl bundle build --architecture linux/arm64 --spec "$(BUNDLE_SPEC)" --output "$(BUNDLE_DIR)" --signing-key "$(SIGNING_KEY)"

check: check-generated check-runtime-source lint test-unit test-contract
