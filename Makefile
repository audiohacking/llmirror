BINARY := llmirror
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build build-all clean test

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/llmirror

build-all: clean
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-darwin-arm64 ./cmd/llmirror
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-amd64 ./cmd/llmirror
	GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY)-linux-arm64 ./cmd/llmirror

clean:
	rm -rf dist $(BINARY)

test:
	go test ./...
