.PHONY: build run test clean dev

# Variables
BINARY_NAME=phishkit-email-service
GO_FILES=$(shell find . -name '*.go' -not -path './vendor/*')

# Development
dev:
	@echo "🚀 Starting development server..."
	@air -c .air.toml || go run cmd/email-api/main.go

# Build
build:
	@echo "🔨 Building $(BINARY_NAME)..."
	@go build -o bin/$(BINARY_NAME) cmd/email-api/main.go

# Run
run: build
	@echo "▶️  Running $(BINARY_NAME)..."
	@./bin/$(BINARY_NAME)

# Test
test:
	@echo "🧪 Running tests..."
	@go test -v ./...

test-coverage:
	@echo "📊 Running tests with coverage..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html

# Clean
clean:
	@echo "🧹 Cleaning..."
	@rm -rf bin/
	@rm -f coverage.out coverage.html
	@go clean

# Dependencies
deps:
	@echo "📦 Installing dependencies..."
	@go mod download
	@go mod tidy

# Lint
lint:
	@echo "🔍 Linting..."
	@golangci-lint run

# Docker
docker-build:
	@echo "🐳 Building Docker image..."
	@docker build -t $(BINARY_NAME) .

# Help
help:
	@echo "Available commands:"
	@echo "  dev          - Start development server"
	@echo "  build        - Build the application"
	@echo "  run          - Build and run the application"
	@echo "  test         - Run tests"
	@echo "  test-coverage- Run tests with coverage"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Install dependencies"
	@echo "  lint         - Run linter"
	@echo "  docker-build - Build Docker image"
