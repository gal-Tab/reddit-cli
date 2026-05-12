.PHONY: build test lint install clean

build:
	go build -o bin/reddit-cli ./cmd/reddit-cli

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/reddit-cli

clean:
	rm -rf bin/

build-mcp:
	go build -o bin/reddit-cli-mcp ./cmd/reddit-cli-mcp

install-mcp:
	go install ./cmd/reddit-cli-mcp

build-all: build build-mcp
