GOCACHE ?= /tmp/vimwhat-go-build

.PHONY: run build test test-race lint

run:
	GOCACHE=$(GOCACHE) go run ./cmd/vimwhat

build:
	GOCACHE=$(GOCACHE) go build -o ./vimwhat ./cmd/vimwhat

test:
	GOCACHE=$(GOCACHE) go test ./...

test-race:
	GOCACHE=$(GOCACHE) go test -race ./...

lint:
	GOCACHE=$(GOCACHE) go vet ./...
