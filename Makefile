API_DIR := host-panel-api
WEB_DIR := host-panel-interface

.PHONY: help install dev api web sftpgo sftpgo-logs down clean

## Development

dev: sftpgo ## Start SFTPGo (compose), the API and the interface
	@echo "→ API: http://localhost:2538   Interface: http://localhost:5173"
	@trap 'kill 0' INT TERM; \
		( cd $(API_DIR) && go run ./cmd/server ) & \
		( cd $(WEB_DIR) && yarn dev ) & \
		wait

api: ## Run the API only (Go backend)
	cd $(API_DIR) && go run ./cmd/server

web: ## Run the interface only (React/Vite)
	cd $(WEB_DIR) && yarn dev

## SFTPGo (Docker)

sftpgo: ## Start SFTPGo in the background
	docker compose up -d sftpgo

sftpgo-logs: ## Follow the SFTPGo logs
	docker compose logs -f sftpgo

down: ## Stop SFTPGo
	docker compose down

## Utilities

install: ## Install the dependencies (API and interface)
	cd $(API_DIR) && go mod download
	cd $(WEB_DIR) && yarn install

clean: ## Stop SFTPGo and delete its local data
	docker compose down
	rm -rf data/sftpgo

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
