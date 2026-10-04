# healthMonitorNJ — see docs/ for what each stage does.
BINARY      := hm
PKG         := github.com/Sznapsollo/health-monitor-nj
VERSION     ?= $(shell cat VERSION 2>/dev/null || echo dev)
COMMIT      ?= $(shell git describe --always --dirty --abbrev=7 2>/dev/null || echo none)
LDFLAGS     := -s -w -X $(PKG)/internal/buildinfo.Version=$(VERSION) -X $(PKG)/internal/buildinfo.Commit=$(COMMIT)
GO          := go
NPM         := npm --prefix web
IMAGE       ?= health-monitor-nj:$(VERSION)

export GOTOOLCHAIN ?= auto
export CGO_ENABLED ?= 0

# Dev ports, passed to `make dev` only so they never leak into tests. Override
# them when something else already holds these ports:
#   make dev HM_HTTP_ADDR=:8191 HM_UDP_ADDR=:8192 HM_ADMIN_ADDR=127.0.0.1:8193
HM_HTTP_ADDR    ?= :8081
HM_UDP_ADDR     ?= :8082
HM_ADMIN_ADDR   ?= 127.0.0.1:8083
HM_PROXY_TARGET ?= http://127.0.0.1$(HM_HTTP_ADDR)
SOAK_CONFIG     ?= soak.yaml
DEV_ENV = HM_OPEN=$${HM_OPEN:-1} HM_HTTP_ADDR=$(HM_HTTP_ADDR) HM_UDP_ADDR=$(HM_UDP_ADDR) HM_ADMIN_ADDR=$(HM_ADMIN_ADDR)

.PHONY: help dev dev-server dev-server-once dev-web build web-build web-install test test-go test-web \
        lint lint-go lint-web fmt fmt-check vet gen gen-check load soak up up-soak docker clean tidy ci

help: ## List the targets
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

dev: ## Run the server and the Vite dev server together (Ctrl-C stops both)
	@$(DEV_ENV) ./scripts/dev-server.sh --check
	@$(MAKE) -j2 dev-server dev-web

dev-server: ## Run the Go server with debug logging, restarting on a change
	$(DEV_ENV) HM_LOG_LEVEL=debug HM_LOG_FORMAT=text ./scripts/dev-server.sh

dev-server-once: ## Run the Go server without the watcher
	$(DEV_ENV) HM_LOG_LEVEL=debug HM_LOG_FORMAT=text $(GO) run ./cmd/hm

dev-web: web-install ## Run the SPA with hot reload on :5173, proxying to the server
	HM_PROXY_TARGET=$(HM_PROXY_TARGET) $(NPM) run dev

build: web-build ## Build the static binary with the SPA embedded
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/hm

web-install: ## Install the web dependencies when they are missing
	@[ -d web/node_modules ] || $(NPM) install

web-build: web-install ## Build the SPA into web/dist
	$(NPM) run build

test: test-go test-web ## Run every test

test-go: ## Go tests with the race detector (which needs cgo, unlike the build)
	CGO_ENABLED=1 $(GO) test -race ./...

test-web: web-install ## Web unit tests
	$(NPM) run test

lint: lint-go lint-web ## Lint both sides

lint-go: ## golangci-lint (falls back to go vet when it is not installed)
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed; running go vet only"; $(GO) vet ./...; \
	fi

lint-web: web-install ## eslint + prettier check + tsc
	$(NPM) run lint
	$(NPM) run format:check
	$(NPM) run typecheck

GOFILES = $(shell git ls-files '*.go')

fmt: ## Format both sides
	gofmt -w $(GOFILES)
	$(NPM) run format

fmt-check: ## Fail when Go files are not gofmt-clean
	@out="$$(gofmt -l $(GOFILES))"; \
	if [ -n "$$out" ]; then echo "not gofmt-clean:"; echo "$$out"; exit 1; fi

vet: ## go vet
	$(GO) vet ./...

gen: ## Regenerate schema/*.json and web/src/protocol.ts from internal/protocol
	$(GO) run ./cmd/gen-schema
	@$(MAKE) web-install
	$(NPM) run gen:protocol

gen-check: gen ## Fail when the committed generated files are stale
	@git diff --exit-code -- schema web/src/protocol.ts \
		|| (echo "generated files are stale; run 'make gen' and commit the result"; exit 1)

load: ## Run the UDP load harness against a running server
	$(GO) run ./cmd/loadgen -addr 127.0.0.1$(HM_UDP_ADDR) -status http://127.0.0.1$(HM_HTTP_ADDR) $(LOADGEN_ARGS)

soak: ## Send the traffic described in $(SOAK_CONFIG) at a running server
	$(GO) run ./cmd/soak -config $(SOAK_CONFIG) -addr 127.0.0.1$(HM_UDP_ADDR) -status http://127.0.0.1$(HM_HTTP_ADDR) $(SOAK_ARGS)

up: ## Rebuild the Docker image from this tree and replace the running container (data kept)
	scripts/docker-up.sh

up-soak: ## Same as up, plus the soak sender
	scripts/docker-up.sh soak

docker: ## Build the container image
	docker build -t $(IMAGE) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) .

tidy: ## go mod tidy
	$(GO) mod tidy

ci: fmt-check vet lint test gen-check ## What CI runs

clean: ## Remove build output
	rm -rf bin web/dist/assets web/dist/index.html
