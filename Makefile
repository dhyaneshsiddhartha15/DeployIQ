VERSION ?= dev
REPO    ?= coredgeio
IMG      = $(REPO)/deployiq-api:$(VERSION)

COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    := $(shell git log -1 --format=%cI 2>/dev/null || echo unknown)

# Injected into pkg/version at link time so both binaries can report what
# they are without a generated file in the tree.
LDFLAGS := -s -w \
	-X github.com/coredgeio/deployiq-optimizer/pkg/version.Version=$(VERSION) \
	-X github.com/coredgeio/deployiq-optimizer/pkg/version.Commit=$(COMMIT) \
	-X github.com/coredgeio/deployiq-optimizer/pkg/version.BuildDate=$(DATE)

.PHONY: all tidy fmt vet lint test test-race test-fixtures build build-cli build-api \
        cross proto docker-build docker-push compose-up compose-down clean help

all: fmt vet test build

## tidy: sync go.mod/go.sum with the source tree
tidy:
	go mod tidy

## fmt: format all Go sources
fmt:
	go fmt ./...

## vet: run the standard vet suite (CI gate, Phase 8.1)
vet:
	go vet ./...

## lint: run golangci-lint (install: https://golangci-lint.run)
lint:
	golangci-lint run ./...

## test: unit tests
# No -race here: the detector needs cgo, and Windows is a supported
# development platform where a C toolchain is often absent. CI runs test-race
# on Linux, so the coverage is not lost.
test:
	go test ./... -count=1

## test-race: unit tests under the race detector (requires cgo)
test-race:
	CGO_ENABLED=1 go test ./... -race -count=1

## test-fixtures: the Phase 10.2 gate — every fixture repo must produce a
#
# Guards two silent-success modes, both worse than a red build because both
# report green while verifying nothing:
#
#   1. No fixtures at all. Legitimate today — the detector does not exist yet
#      — but it must be said out loud, not inferred from a green tick.
#   2. Fixtures present but no test running them. `go test -run TestFixtures`
#      exits 0 with "no tests to run", so the exit code alone cannot be
#      trusted; the output is grepped.
#
# One shell for the whole recipe (note the trailing `; \` after `fi`): make
# runs each recipe *line* in its own shell, so a bare `exit 0` on the first
# line would end that subshell and let the rest of the target run anyway.
test-fixtures:
	@if [ -z "$$(find testdata/fixtures -mindepth 1 -maxdepth 1 -type d 2>/dev/null)" ]; then \
		echo "=============================================================="; \
		echo "GATE NOT ENFORCED: testdata/fixtures/ contains no fixtures."; \
		echo "The Phase 10.2 release gate is not testing anything yet."; \
		echo "This is expected until Phase 1 of the build plan lands the"; \
		echo "detector. It is NOT a passing run of the gate."; \
		echo "=============================================================="; \
		exit 0; \
	fi; \
	out=$$(go test ./... -run TestFixtures -count=1 -tags=fixtures 2>&1); \
	status=$$?; \
	echo "$$out"; \
	if echo "$$out" | grep -q "no tests to run"; then \
		echo "FAIL: fixtures exist but no TestFixtures test ran."; \
		exit 1; \
	fi; \
	exit $$status

## build: both binaries into ./bin
build: build-cli build-api

## build-cli: the doiq CLI — the v1 product (single static binary, no cgo)
build-cli:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/doiq ./cmd/doiq

## build-api: the optional backend API server (Phase 4+ of the build plan)
build-api:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/doiq-api ./cmd/doiq-api

## cross: cross-compile the CLI for every platform promised in Phase 1.2
cross:
	@mkdir -p dist
	@set -e; for target in \
		darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64; do \
		os=$${target%/*}; arch=$${target#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "building dist/doiq-$$os-$$arch$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath \
			-ldflags '$(LDFLAGS)' -o dist/doiq-$$os-$$arch$$ext ./cmd/doiq; \
	done

## proto: regenerate protobuf, gRPC, gateway and OpenAPI code from api/v1
proto:
	go generate ./api/...

## docker-build: container image for the API server (the CLI ships as a binary)
docker-build:
	docker build -t $(IMG) -f build/Dockerfile .

## docker-push: publish the API server image
docker-push:
	docker push $(IMG)

## compose-up: local API + MongoDB for development
compose-up:
	docker compose -f build/docker-compose.yml up -d

## compose-down: tear down the local development stack
compose-down:
	docker compose -f build/docker-compose.yml down -v

## clean: remove build output
clean:
	rm -rf bin dist

## help: list available targets
# Matches only "## <target>: description" so a multi-line explanatory comment
# elsewhere in this file cannot masquerade as a target in the listing.
help:
	@grep -E '^## [a-z][a-z0-9-]*:' $(MAKEFILE_LIST) | sed 's/^## //'
