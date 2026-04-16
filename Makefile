# Plekt Makefile

MODULE := github.com/plekt-dev/plekt
COVER_RAW  := /tmp/mc_cover_raw.out
COVER_FILT := /tmp/mc_cover_filtered.out

.PHONY: build test coverage coverage-html lint clean e2e-install e2e e2e-headed e2e-ui test-all

## build: compile the binary
build:
	go build ./...

## test: run all tests without coverage
test:
	go test ./... -count=1

## coverage: run tests, filter generated *_templ.go files, report per-function coverage
##
## Go has no built-in flag to exclude individual files from a coverage profile.
## The standard approach for generated code (used by the templ project itself) is
## to post-process the raw profile:
##   1. Collect the raw profile with -coverprofile.
##   2. Pipe through grep -v to drop every line referencing *_templ.go.
##   3. Feed the filtered profile to `go tool cover -func`.
##
## This keeps the generated files part of the normal build (they are not ignored),
## while removing their structurally-uncoverable WriteString error branches from
## the reported metrics.
coverage:
	go test -coverprofile=$(COVER_RAW) -covermode=atomic ./... -count=1
	@grep -v "_templ\.go:" $(COVER_RAW) > $(COVER_FILT)
	@echo ""
	@echo "=== Coverage (generated *_templ.go files excluded) ==="
	@go tool cover -func=$(COVER_FILT)

## coverage-html: open an HTML coverage report (templ files excluded)
coverage-html: coverage
	go tool cover -html=$(COVER_FILT)

## lint: run go vet across all packages
lint:
	go vet ./...

## clean: remove compiled artifacts and coverage profiles
clean:
	rm -f $(COVER_RAW) $(COVER_FILT)
	go clean ./...

## e2e-install: install Playwright and browser dependencies
e2e-install:
	npm install
	npx playwright install chromium

## e2e: run Playwright E2E tests (headless)
e2e:
	npx playwright test

## e2e-headed: run Playwright E2E tests in headed mode
e2e-headed:
	npx playwright test --headed

## e2e-ui: open Playwright UI mode
e2e-ui:
	npx playwright test --ui

## test-all: run Go unit tests and Playwright E2E tests
test-all: test e2e
