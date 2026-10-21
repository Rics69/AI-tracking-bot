LINT_IMAGE := golangci/golangci-lint:v2.13.2

RUNS ?= 1

.PHONY: up down logs seed gen test test-short lint eval

up: ## Собрать и запустить всё в Docker
	docker compose up -d --build

down: ## Остановить контейнеры
	docker compose down

logs: ## Логи всех сервисов
	docker compose logs -f

seed: ## Демо-данные для панели: 3 участника в семье ADMIN_TELEGRAM_ID и месяц трат (можно запускать повторно)
	@set -a && . ./.env && set +a && if [ -z "$$ADMIN_TELEGRAM_ID" ]; then echo "Укажите ADMIN_TELEGRAM_ID в .env"; exit 1; fi && \
	docker compose exec -T -e ADMIN="$$ADMIN_TELEGRAM_ID" postgres \
		sh -c 'psql -v ON_ERROR_STOP=1 -v admin="$$ADMIN" -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"' < scripts/demo-seed.sql

gen: ## Сгенерировать код из api/openapi.yaml (Go и TypeScript)
	cd backend && go generate ./...
	cd web && npm run gen:api

test: ## Все тесты, включая интеграционные с Postgres в Docker
	cd backend && go test -race ./...

test-short: ## Только юнит-тесты, без Docker
	cd backend && go test -short ./...

lint: ## go vet + golangci-lint (в Docker, ставить локально не нужно)
	cd backend && go vet ./...
	docker run --rm -v "$(CURDIR)/backend":/app -v "$$(go env GOMODCACHE)":/go/pkg/mod -w /app $(LINT_IMAGE) golangci-lint run ./...

eval: ## Eval агента на настоящем GigaChat (ключи из .env), отчёт в backend/eval/report.md
	set -a && . ./.env && set +a && cd backend && LOG_LEVEL=warn go run ./cmd/eval -runs $(RUNS) -report eval/report.md
