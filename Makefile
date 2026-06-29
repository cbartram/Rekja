MODULE      := github.com/cbartram/rekja
BINARY      := rekja
CMD_DIR     := ./cmd/$(BINARY)
BUILD_DIR   := bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -ldflags "-s -w -X main.version=$(VERSION)"
PROTO_DIR   := proto/rekja/v1

# Paths to custom-installed tools (in sandboxed environments)
PROTOC     ?= /tmp/protoc-install/bin/protoc
GO         ?= go
GOPATH     ?= /tmp/go-mod-cache
PROTO_GO_BIN := $(GOPATH)/bin

.PHONY: build run test test-verbose test-cover lint fmt vet tidy clean proto sidecar

proto:
	@command -v $(PROTOC) >/dev/null || (echo "protoc not found at $(PROTOC); download it to /tmp/protoc-install/bin/protoc" && exit 1)
	@$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest 2>/dev/null || true
	@$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest 2>/dev/null || true
	$(PROTOC) -I . --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		$(PROTO_DIR)/rekja.proto

build: proto
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

run:
	$(GO) run $(CMD_DIR)

sidecar: proto
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(LDFLAGS) -o $(BUILD_DIR)/rekja-sidecar ./sidecar

test:
	$(GO) test -v -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

fmt:
	gofmt -l -w .

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

install:
	$(GO) install $(LDFLAGS) $(CMD_DIR)

clean:
	rm -rf $(BUILD_DIR) coverage.out
