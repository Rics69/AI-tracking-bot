LINT_IMAGE := golangci/golangci-lint:v2.13.2

.PHONY: up down logs gen test test-short lint

up: ## Собрать и запустить всё в Docker
	docker compose up -d --build

down: ## Остановить контейнеры
	docker compose down

logs: ## Логи всех сервисов
	docker compose logs -f

gen: ## Сгенерировать код из api/openapi.yaml
	cd backend && go generate ./...

test: ## Все тесты, включая интеграционные с Postgres в Docker
	cd backend && go test -race ./...

test-short: ## Только юнит-тесты, без Docker
	cd backend && go test -short ./...

lint: ## go vet + golangci-lint (в Docker, ставить локально не нужно)
	cd backend && go vet ./...
	docker run --rm -v "$(CURDIR)/backend":/app -v "$$(go env GOMODCACHE)":/go/pkg/mod -w /app $(LINT_IMAGE) golangci-lint run ./...
