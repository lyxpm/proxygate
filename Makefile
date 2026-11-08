GO ?= go
PNPM ?= pnpm
BIN ?= bin/proxygate
VERSION ?= 0.1.1

.PHONY: all web build test vet fmt run docker clean

all: test build

web:
	cd web && $(PNPM) install --frozen-lockfile && $(PNPM) build

build: web
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags "-s -w" -o $(BIN) ./cmd/server

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w cmd internal migrations

run:
	PG_DATA_DIR=./data $(GO) run ./cmd/server

docker:
	docker build -t proxygate:latest .

docker-buildx:
	docker buildx build --platform linux/amd64,linux/arm64 -t proxygate:latest .

clean:
	rm -rf bin web/dist

