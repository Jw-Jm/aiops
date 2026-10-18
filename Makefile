GO ?= go
PYTHON ?= python3
UV ?= uv
GO_SOURCES := $(shell find cmd internal gen test -type f -name '*.go' 2>/dev/null)
GO_UNIT_PACKAGES := ./cmd/... ./internal/... ./gen/...
# Explicit suite selection keeps local checks independent of live flags and load.
CAPACITY_TESTS := TestUpstreamConformanceAnd20KPlatformGeneration|TestDiagnosticGraphAt200NodeBudgetLatency|TestPublishedGraphQueriesContinueDuringLargeRebuild|TestAriadneNonVirtualGolden20K
NATIVE_TESTS := TestSP07ActualBusinessValues|TestNativeFixedCLIThroughScopedReadBroker
REPLAY_TESTS := TestGraphNonVirtualUpstreamReplay|TestInspectionNonVirtualUpstreamReplay
BROWSER_RUNTIME_TEST := TestAssistantActualOIDCAndBrowserRuntime
LIVE_CAPACITY_TESTS := TestAssistantActualCollectedSourcesAndCapacity|TestFormalCurrentTenInvestigationsAndMainline
LIVE_SOURCE_TESTS := TestAssistantActualDeepFlowL4AndL7|TestAssistantActualFaultChainWithoutModel|TestAssistantOperationalActualKubernetesObservations

.PHONY: bootstrap check-toolchain generate check-generated check-runtime-source check-delivery fmt lint test-local test-unit test-contract test-web test-python test-integration test-live-sources test-native test-capacity test-capacity-live test-browser-runtime test-web-e2e test-replay test-e2e test-security bundle-dev-arm64 verify-bundle check-local check

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
	python3 scripts/generate-assistant-openapi.py
	$(GO) generate ./...

check-generated:
	python3 scripts/check-generated.py "$(GO)" gen web/src/api/generated internal/persistence/dbgen services/investigator/src/investigator/output.schema.json services/investigator/src/investigator/assistant-output.schema.json

fmt:
	gofmt -w $(GO_SOURCES)

check-runtime-source:
	python3 scripts/prepare-runtime-source.py --check
	python3 scripts/prepare-migration-source.py --check

check-delivery: check-generated check-runtime-source
	$(GO) test ./test/contract -run '^TestDeliveryPreparation' -count=1

lint:
	$(GO) vet ./...

test-unit:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test -tags='' -timeout=10m $(GO_UNIT_PACKAGES) -skip '^($(CAPACITY_TESTS)|$(NATIVE_TESTS))$$' > artifacts/test-reports/unit.txt 2>&1; status=$$?; cat artifacts/test-reports/unit.txt; exit $$status

test-web:
	pnpm --dir web exec tsc --noEmit
	pnpm --dir web test

test-python:
	cd services/investigator && $(UV) run --offline --locked --group dev python -m pytest
	$(PYTHON) -m unittest discover -s scripts/deepflow-dev -p 'test_*.py'
	$(PYTHON) -m unittest discover -s scripts/assistant-eval -p 'test_*.py'
	$(PYTHON) -m unittest discover -s scripts/assistant-runtime -p 'test_*.py'

test-local: test-unit test-contract test-web test-python test-security

test-contract:
	@if [ -d test/contract ] && find test/contract -type f -name '*_test.go' -print -quit | grep -q .; then \
		mkdir -p artifacts/test-reports; \
		set +e; $(GO) test -tags='' ./test/contract -skip '^($(REPLAY_TESTS)|$(CAPACITY_TESTS))$$' > artifacts/test-reports/contract.txt 2>&1; status=$$?; \
		cat artifacts/test-reports/contract.txt; exit $$status; \
	else \
		echo "No Contract tests are defined yet."; \
	fi

test-e2e:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/e2e -count=1 -json -skip '^TestKubeVirtPOCArtifactsAndOrbStackLifecycle$$' -timeout=20m > artifacts/test-reports/e2e.jsonl 2>&1; status=$$?; cat artifacts/test-reports/e2e.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/e2e.jsonl

test-integration:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/integration -count=1 -json -timeout=10m -skip '^(TestAssistantCrashWorkerChild|TestSP05OutboxChildProcessClaim|$(LIVE_SOURCE_TESTS)|$(BROWSER_RUNTIME_TEST)|$(LIVE_CAPACITY_TESTS))$$' > artifacts/test-reports/integration.jsonl 2>&1; status=$$?; cat artifacts/test-reports/integration.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/integration.jsonl

test-live-sources:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/integration -run '^($(LIVE_SOURCE_TESTS))$$' -count=1 -json -timeout=10m > artifacts/test-reports/live-sources.jsonl 2>&1; status=$$?; cat artifacts/test-reports/live-sources.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/live-sources.jsonl

test-native:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test $(GO_UNIT_PACKAGES) -run '^($(NATIVE_TESTS))$$' -count=1 -json -timeout=10m > artifacts/test-reports/native.jsonl 2>&1; status=$$?; cat artifacts/test-reports/native.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/native.jsonl

test-capacity:
	@test -z "$$OPS_PERFORMANCE_EXEMPTION" || { echo "capacity acceptance cannot use OPS_PERFORMANCE_EXEMPTION" >&2; exit 1; }
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./internal/graph ./test/contract -run '^($(CAPACITY_TESTS))$$' -count=1 -json -timeout=30m > artifacts/test-reports/capacity.jsonl 2>&1; status=$$?; cat artifacts/test-reports/capacity.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/capacity.jsonl

test-capacity-live:
	@test -z "$$OPS_PERFORMANCE_EXEMPTION" || { echo "capacity acceptance cannot use OPS_PERFORMANCE_EXEMPTION" >&2; exit 1; }
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test -tags=pre_sp07_live ./test/integration -run '^($(LIVE_CAPACITY_TESTS))$$' -count=1 -json -timeout=30m > artifacts/test-reports/capacity-live.jsonl 2>&1; status=$$?; cat artifacts/test-reports/capacity-live.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/capacity-live.jsonl

test-browser-runtime:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./test/integration -run '^$(BROWSER_RUNTIME_TEST)$$' -count=1 -json -timeout=20m > artifacts/test-reports/browser-runtime.jsonl 2>&1; status=$$?; cat artifacts/test-reports/browser-runtime.jsonl; test $$status -eq 0 || exit $$status; python3 scripts/check-test-report.py artifacts/test-reports/browser-runtime.jsonl

test-web-e2e:
	@test -n "$$OPS_WEB_QA_INPUT" || { echo "OPS_WEB_QA_INPUT is required; see web/tests/e2e/session.ts"; exit 1; }
	@mkdir -p artifacts/test-reports
	@set -e; report="$${OPS_WEB_QA_REPORT:-$(CURDIR)/artifacts/test-reports/web-e2e.json}"; \
		rm -f "$$report"; \
		OPS_WEB_QA_REPORT="$$report" pnpm --dir web test:e2e; \
		$(PYTHON) scripts/check-test-report.py --playwright "$$report"

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

check-local: lint test-local

check: check-generated check-runtime-source check-local
