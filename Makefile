# Makefile for testscript project

BUILD_DIR := build
BINARY_NAME := tsar

.PHONY: all build test clean fmt vet mod-tidy help

# Default target
all: test

# Build the project
build:
	mkdir -p $(BUILD_DIR)
	go build -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/tsar

# Run tests
test:
	go test -v ./...

# Clean build artifacts  
clean:
	rm -rf $(BUILD_DIR)
	go clean

# Format code
fmt:
	go fmt ./...

# Vet code
vet:
	go vet ./...

# Tidy modules
mod-tidy:
	go mod tidy

# Show help
help:
	@echo "Available targets:"
	@echo "  all      - Run tests (default)"
	@echo "  build    - Build the $(BINARY_NAME) binary to $(BUILD_DIR)/"
	@echo "  test     - Run all tests"
	@echo "  clean    - Clean build artifacts"
	@echo "  fmt      - Format code"
	@echo "  vet      - Vet code"
	@echo "  mod-tidy - Tidy Go modules"
	@echo "  help     - Show this help message"