# Self-documenting Makefile: `make` or `make help` lists targets.
APP        := ingest-service
IMAGE      ?= $(APP):dev
REGISTRY   ?= registry.digitalocean.com/<your-registry>
PORT       ?= 8080

.DEFAULT_GOAL := help

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: run
run: ## Run the server locally
	go run ./cmd/server

.PHONY: test
test: ## Run all tests with the race detector
	go test -race -count=1 ./...

.PHONY: cover
cover: ## Run tests and open an HTML coverage report
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

.PHONY: lint
lint: ## Run go vet and golangci-lint (if installed)
	go vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

.PHONY: tidy
tidy: ## Sync go.mod/go.sum
	go mod tidy

.PHONY: build
build: ## Build the binary into ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/server ./cmd/server

.PHONY: docker
docker: ## Build the container image
	docker build -t $(IMAGE) .

.PHONY: docker-run
docker-run: docker ## Build and run the container locally
	docker run --rm -p $(PORT):8080 -e PORT=8080 $(IMAGE)

.PHONY: push
push: ## Tag and push the image to the DO container registry
	docker tag $(IMAGE) $(REGISTRY)/$(APP):latest
	docker push $(REGISTRY)/$(APP):latest

.PHONY: smoke
smoke: ## Hit the running service with a sample request (PORT overridable)
	curl -s -XPOST localhost:$(PORT)/v1/events \
		-H 'Content-Type: application/json' \
		-d '{"type":"click","source":"web","payload":{"value":21}}' | tee /dev/stderr
	@echo
	curl -s localhost:$(PORT)/v1/stats
