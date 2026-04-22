GOCACHE ?= /tmp/vimwhat-go-build

.PHONY: run build test lint

run:
	GOCACHE=$(GOCACHE) go run ./cmd/vimwhat

build:
	GOCACHE=$(GOCACHE) go build -o ./vimwhat ./cmd/vimwhat

test:
	GOCACHE=$(GOCACHE) go test ./...

lint:
	GOCACHE=$(GOCACHE) go vet ./...
