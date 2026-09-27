GO ?= go
GO_SOURCES := $(shell find . -type f -name '*.go' -not -path './.git/*')

.PHONY: bootstrap check-toolchain generate check-generated fmt lint test-unit test-contract test-integration test-replay test-e2e test-security bundle-dev-arm64 verify-bundle check

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

check-generated: generate
	@if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then \
		if [ -n "$$(git status --porcelain --untracked-files=all -- gen)" ]; then \
			git status --short --untracked-files=all -- gen >&2; \
			echo "Generated artifacts are out of date; run make generate and review the gen/ changes." >&2; \
			exit 1; \
		fi; \
	else \
		echo "check-generated requires the standalone platform Git repository" >&2; \
		exit 1; \
	fi

fmt:
	gofmt -w $(GO_SOURCES)

lint:
	$(GO) vet ./...

test-unit:
	@mkdir -p artifacts/test-reports
	@set +e; $(GO) test ./... > artifacts/test-reports/unit.txt 2>&1; status=$$?; cat artifacts/test-reports/unit.txt; exit $$status

test-contract:
	@if [ -d test/contract ] && find test/contract -type f -name '*_test.go' -print -quit | grep -q .; then \
		mkdir -p artifacts/test-reports; \
		set +e; $(GO) test ./test/contract > artifacts/test-reports/contract.txt 2>&1; status=$$?; \
		cat artifacts/test-reports/contract.txt; exit $$status; \
	else \
		echo "No Contract tests are defined yet."; \
	fi

test-integration test-replay test-e2e test-security:
	@echo "$@ suite is not implemented yet."

bundle-dev-arm64 verify-bundle:
	@echo "$@ is not implemented yet." >&2
	@exit 1

check: check-generated lint test-unit test-contract
