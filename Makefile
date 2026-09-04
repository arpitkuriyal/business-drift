.DEFAULT_GOAL := help

.PHONY: help install infra-up up down logs migrate-up migrate-down run test lint build compose-check ci

help: ## Show available commands
	@awk 'BEGIN {FS = ":.*## "; printf "Business Drift commands:\n"} /^[a-zA-Z_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

install: ## Download Go and web dependencies
	go mod download
	npm ci --prefix web

infra-up: ## Start PostgreSQL and Redis
	docker compose up -d --wait postgres redis

up: ## Build and start the API, PostgreSQL, and Redis
	docker compose up -d --build --wait api postgres redis

down: ## Stop local services without deleting data
	docker compose down

logs: ## Follow API and dependency logs
	docker compose logs -f api postgres redis

migrate-up: infra-up ## Apply all database migrations
	docker compose run --rm migrate -path=/migrations -database='postgres://business_drift:business_drift@postgres:5432/business_drift?sslmode=disable' up

migrate-down: infra-up ## Roll back one database migration
	docker compose run --rm migrate -path=/migrations -database='postgres://business_drift:business_drift@postgres:5432/business_drift?sslmode=disable' down 1

run: ## Run the API on the host
	go run ./cmd/api

test: ## Run Go tests with the race detector
	go test -race ./...

lint: ## Check formatting, Go code, and web code
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && exit 1)
	go vet ./...
	npm run lint --prefix web

build: ## Build the API and production web assets
	mkdir -p bin
	go build -trimpath -o bin/api ./cmd/api
	npm run build --prefix web

compose-check: ## Validate the Compose configuration
	docker compose config --quiet

ci: lint test build compose-check ## Run the local CI-equivalent checks
