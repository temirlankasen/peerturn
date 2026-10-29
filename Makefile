GO ?= go
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0
ACTIONLINT ?= $(GO) run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
GORELEASER ?= $(GO) run github.com/goreleaser/goreleaser/v2@v2.18.3

VERSION ?= dev
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || printf unknown)
BUILD_DATE ?= $(shell git log -1 --format=%cI 2>/dev/null || printf unknown)
VERSION_PACKAGE := github.com/temirlankasen/peerturn/internal/version
LDFLAGS := -s -w -X $(VERSION_PACKAGE).Version=$(VERSION) -X $(VERSION_PACKAGE).Commit=$(COMMIT) -X $(VERSION_PACKAGE).Date=$(BUILD_DATE)

.PHONY: build test vet lint vuln fmt fmt-check tidy tidy-check workflow-check release-check public-check ci runtime-contract-check runtime-module-check runtime-automation-check

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/peerturn ./cmd/peerturn

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

lint:
	$(GOLANGCI_LINT) run

vuln:
	$(GOVULNCHECK) ./...

fmt:
	gofmt -w cmd internal

fmt-check:
	@files="$$(gofmt -l cmd internal)"; if [ -n "$$files" ]; then printf '%s\n' "$$files"; exit 1; fi

tidy:
	$(GO) mod tidy

tidy-check:
	$(GO) mod tidy -diff

workflow-check:
	$(ACTIONLINT)

release-check:
	$(GORELEASER) check

public-check:
	python3 -I -B scripts/install_test.py
	python3 -I -B scripts/release_test.py
	python3 -I -B scripts/publish_check_test.py
	python3 -I -B scripts/export_public_test.py
	sh scripts/publish-check.sh

runtime-contract-check: runtime-module-check
	$(GO) test -race ./internal/runtimepolicy ./internal/adapters/codex ./internal/claudeipc ./internal/mcp

runtime-module-check:
	node --test adapters/claude/tests/*.test.mjs

runtime-automation-check:
	python3 -I -B scripts/runtime_automation_test.py
	python3 -I -B scripts/runtime_watch_test.py
	python3 -I -B scripts/runtime_promote_test.py
	python3 -I -B scripts/runtime_promotion_security_test.py
	python3 -I -B scripts/runtime_evidence_test.py
	python3 -I -B scripts/runtime_install_test.py
	python3 -I -B scripts/runtime_npm_test.py
	python3 -I -B scripts/runtime_report_test.py
	python3 -I -B research/lab/qualify/test_qualification.py

ci: fmt-check tidy-check build vet test lint vuln workflow-check release-check public-check runtime-module-check runtime-automation-check
