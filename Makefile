GOCACHE ?= /tmp/vimwhat-go-build

.PHONY: run build test test-windows lint

run:
	GOCACHE=$(GOCACHE) go run ./cmd/vimwhat

build:
	GOCACHE=$(GOCACHE) go build -o ./vimwhat ./cmd/vimwhat

test:
	GOCACHE=$(GOCACHE) go test ./...

test-windows:
	GOCACHE=$(GOCACHE) GOOS=windows GOARCH=amd64 go test ./... -run ^$ -exec=true

lint:
	GOCACHE=$(GOCACHE) go vet ./...
