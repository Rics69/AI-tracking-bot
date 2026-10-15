# Семейные расходы

Сервис учёта семейных расходов: член семьи пишет Telegram-боту «кофе 350», ИИ-агент сам определяет сумму, категорию и дату. Глава семьи видит и редактирует траты всех в веб-панели.

> Проект в разработке: готовы бэкенд и REST API, бот и веб-панель — следующие этапы.

## Запуск

```bash
cp .env.example .env
docker compose up -d --build
```

| Что | Адрес |
|---|---|
| REST API | http://localhost:8080/api/v1 |
| Swagger UI | http://localhost:8080/docs |
| Health check | http://localhost:8080/healthz |

## Разработка

```bash
make gen         # код из backend/api/openapi.yaml
make test        # все тесты (нужен Docker)
make test-short  # только юнит-тесты
make lint
```
