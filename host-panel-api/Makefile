APP_NAME := host-panel-api
BUILD_DIR := ./build
DOCKER_IMAGE := host-panel-api

# Variables are resolved with this priority: server environment > .env.local > .env > default.
# dotenv reads a key from .env.local, then .env (first value found).
dotenv = $(strip $(or \
	$(shell [ -f .env.local ] && sed -n 's/^[[:space:]]*$(1)=//p' .env.local | tail -1), \
	$(shell [ -f .env ] && sed -n 's/^[[:space:]]*$(1)=//p' .env | tail -1)))

# resolve sets $(1) only when it does not come from the server environment
# or the command line (which take precedence): from the .env* files, else
# the default value $(2). The variable is exported.
define resolve
ifeq ($(filter environment command line,$(origin $(1))),)
$(1) := $(or $(call dotenv,$(1)),$(2))
endif
export $(1)
endef

.PHONY: dev build run clean vet fmt tidy docker docker-run test test-integration

## Development

dev: ## Run the server in development mode
	go run ./cmd/server

build: ## Build the binary
	CGO_ENABLED=0 go build -ldflags="-s -w" -o $(BUILD_DIR)/$(APP_NAME) ./cmd/server

run: build ## Build and run
	$(BUILD_DIR)/$(APP_NAME)

## Quality

vet: ## Static analysis
	go vet ./...

fmt: ## Format the code
	gofmt -s -w .

tidy: ## Tidy the dependencies
	go mod tidy

test: ## Run the tests
	go test ./...

test-integration: ## Run the integration tests (Docker required)
	go test -tags integration -count=1 -timeout 20m ./...
	
## Docker

docker: ## Build the Docker image
	docker build -t $(DOCKER_IMAGE) .

docker-run: docker ## Build and run the container
	docker run --rm -p 2538:2538 \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(PWD)/data:/app/data \
		--env-file .env \
		$(DOCKER_IMAGE)

## SFTP

# Priority: server environment > .env.local > .env > default (see resolve above).
# SFTPGO_PASSWORD is deliberately empty by default: it is asked interactively
# (by curl, off the command line, so it does not show in `ps`).
$(eval $(call resolve,SFTP_API_URL,http://localhost:8080))
$(eval $(call resolve,SFTPGO_ADMIN,admin))
$(eval $(call resolve,SFTPGO_PASSWORD,))
$(eval $(call resolve,SFTP_KEY_NAME,host-panel))
SFTPGO_URL ?= $(SFTP_API_URL)

sftp-key: ## Create an SFTPGo admin API key (password asked interactively)
	@command -v jq >/dev/null || { echo "  ✗ jq is required"; exit 1; }
	@if [ -n "$(SFTPGO_PASSWORD)" ]; then \
		token=$$(curl -s -u "$(SFTPGO_ADMIN):$(SFTPGO_PASSWORD)" $(SFTPGO_URL)/api/v2/token | jq -r .access_token); \
	else \
		token=$$(curl -s -u "$(SFTPGO_ADMIN)" $(SFTPGO_URL)/api/v2/token | jq -r .access_token); \
	fi; \
	if [ "$$token" = "null" ] || [ -z "$$token" ]; then echo "  ✗ SFTPGo authentication failed ($(SFTPGO_URL))"; exit 1; fi; \
	key=$$(curl -s -X POST $(SFTPGO_URL)/api/v2/apikeys \
		-H "Authorization: Bearer $$token" \
		-H "Content-Type: application/json" \
		-d '{"name":"$(SFTP_KEY_NAME)","scope":1,"admin":"$(SFTPGO_ADMIN)"}' | jq -r .key); \
	if [ "$$key" = "null" ] || [ -z "$$key" ]; then echo "  ✗ Key creation failed"; exit 1; fi; \
	echo "  ✓ SFTPGo admin API key created. Paste it into .env:"; \
	echo "    SFTP_API_KEY=$$key"

## Utilities

clean: ## Delete the generated files
	rm -rf $(BUILD_DIR)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
