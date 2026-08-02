.PHONY: bootstrap build test dev docker-build

VERSION ?= dev
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)

bootstrap:
	pnpm --dir web install
	go mod download

build:
	pnpm --dir web build
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/Nciae-Zyh/stundeck/internal/version.Version=$(VERSION) -X github.com/Nciae-Zyh/stundeck/internal/version.Commit=$(COMMIT)" -o bin/stundeck ./cmd/stundeck
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/stundeck-notify ./cmd/stundeck-notify

test:
	go test ./...
	pnpm --dir web run typecheck
	pnpm --dir web run test
	bash scripts/check-no-secrets.sh

dev:
	go run ./cmd/stundeck

docker-build:
	docker build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t stundeck:local .
