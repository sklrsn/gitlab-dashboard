VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
GOOS    ?= $(shell go env GOOS)
GOARCH  ?= $(shell go env GOARCH)
OUT     ?= bin/gitlab-dashboard-$(GOOS)-$(GOARCH)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build ui binary test dev-fake dev-server dev-ui docker clean

## build: frontend + single static binary (cross-compile with GOOS/GOARCH)
build: ui binary

ui:
	cd web && npm ci --no-audit --no-fund
	rm -rf web/dist/assets web/dist/index.html
	cd web && npm run build

binary:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags "$(LDFLAGS)" -o $(OUT) ./cmd/gitlab-dashboard
	@echo "built $(OUT)"

test:
	go vet ./...
	go test -race ./...
	cd web && npm run typecheck

## Local development against the fake GitLab (three terminals, or run dev-fake + dev-server).
dev-fake:
	go run ./cmd/fakegitlab

dev-server:
	GITLAB_URL=http://localhost:8929 GITLAB_TOKEN=dev-token DEFAULT_SCOPE=group:platform go run ./cmd/gitlab-dashboard

dev-ui:
	cd web && npm run dev

docker:
	docker build -f deploy/docker/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t gitlab-dashboard:$(VERSION) .

clean:
	rm -rf bin web/dist/assets web/dist/index.html
