GOCACHE ?= /tmp/maybewhats-go-build

.PHONY: run build test lint

run:
	GOCACHE=$(GOCACHE) go run ./cmd/maybewhats

build:
	GOCACHE=$(GOCACHE) go build ./cmd/maybewhats

test:
	GOCACHE=$(GOCACHE) go test ./...

lint:
	GOCACHE=$(GOCACHE) go vet ./...
