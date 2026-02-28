.PHONY: build test lint fmt vet clean install

BINARY := copier
PKG := github.com/fyltr/copier-go
CMD := ./cmd/copier

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-s -w -X $(PKG)/internal/version.Version=$(VERSION)"

build:
	go build $(LDFLAGS) -o $(BINARY) $(CMD)

install:
	go install $(LDFLAGS) $(CMD)

test:
	go test -race -count=1 ./...

test-v:
	go test -race -count=1 -v ./...

test-short:
	go test -short -count=1 ./...

cover:
	go test -race -coverprofile=coverage.txt -covermode=atomic ./...
	go tool cover -html=coverage.txt -o coverage.html

lint: vet
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed; run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

fmt:
	gofmt -s -w .
	goimports -w .

vet:
	go vet ./...

clean:
	rm -f $(BINARY) coverage.txt coverage.html
	go clean -cache -testcache
