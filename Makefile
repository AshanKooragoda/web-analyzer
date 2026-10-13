APP     := web-analyzer
BIN     := bin/server
IMAGE   := web-analyzer:latest
PORT    ?= 8080

.PHONY: help run build test test-integration cover lint fmt tidy docker-build docker-run clean

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-18s %s\n", $$1, $$2}'

run: ## Run the server locally (PORT=8080 by default)
	ADDR=:$(PORT) go run ./cmd/server

build: ## Build the binary into bin/
	CGO_ENABLED=0 go build -trimpath -o $(BIN) ./cmd/server

test: ## Run unit tests with the race detector
	go test -race ./...

test-integration: ## Run integration tests (needs internet access)
	go test -race -tags=integration ./...

cover: ## Run unit tests and write coverage.html
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out -o coverage.html

lint: ## Run go vet and golangci-lint (if installed)
	go vet ./...
	@command -v golangci-lint >/dev/null && golangci-lint run ./... || echo "golangci-lint not installed, skipped"

fmt: ## Format the code
	gofmt -w .

tidy: ## Tidy go.mod and go.sum
	go mod tidy

docker-build: ## Build the Docker image
	docker build -t $(IMAGE) .

docker-run: docker-build ## Build and run the Docker image (PORT=8080 by default)
	docker run --rm -p $(PORT):8080 $(IMAGE)

clean: ## Remove build and coverage output
	rm -rf bin coverage.out coverage.html
