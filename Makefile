MODULE      := github.com/cbartram/rekja
BINARY      := rekja
CMD_DIR     := ./cmd/$(BINARY)
BUILD_DIR   := bin
VERSION     := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS     := -ldflags "-s -w -X main.version=$(VERSION)"

.PHONY: build run test test-verbose test-cover lint fmt vet tidy clean install

build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

run:
	go run $(CMD_DIR)

test:
	go test -v -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

lint: vet
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run ./... || echo "golangci-lint not installed, skipping"

fmt:
	gofmt -l -w .

vet:
	go vet ./...

tidy:
	go mod tidy

install:
	go install $(LDFLAGS) $(CMD_DIR)

clean:
	rm -rf $(BUILD_DIR) coverage.out