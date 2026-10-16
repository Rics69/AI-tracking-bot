# CLAUDE.md

Сервис учёта семейных расходов: Telegram-бот с ИИ-агентом (GigaChat) + веб-панель главы семьи.
ТЗ — `test-task-expenses.md`.

## Структура

- `backend/` — Go-модуль, бинари в `cmd/`:
  - `cmd/api` — REST API для веб-панели.
  - `cmd/bot` — Telegram-бот с агентом (long polling).
  - `cmd/chat` — консольный чат с агентом без Telegram, для отладки промпта.
  - `cmd/eval` — eval агента на настоящем LLM: сценарии в `backend/eval/cases.yaml`, каждый на чистом in-memory хранилище.
- `backend/internal/expense` — домен трат: сущности, валидация, `Service`. Без SQL и HTTP.
- `backend/internal/llm` — интерфейс модели, не зависящий от провайдера; `llm/gigachat` — клиент GigaChat (OAuth, сертификат Минцифры, function calling); `llm/provider` — выбор провайдера по `LLM_PROVIDER`.
- `backend/internal/agent` — агент: тулы над `expense.Service`, системный промпт, цикл tool calling, история диалога.
- `backend/internal/telegram` — обработчик апдейтов: белый список, очередь по пользователю, «печатает…».
- `backend/internal/user` — члены семьи (Telegram-пользователи).
- `backend/internal/storage/postgres` — репозитории на pgx, миграции goose (вшиты через embed, применяются при старте).
- `backend/internal/storage/memory` — in-memory репозиторий трат с той же семантикой (для eval).
- `backend/internal/httpapi` — REST: `api.gen.go` генерируется из `backend/api/openapi.yaml`, руками пишется `server.go`.
- `web/` — Next.js 16 панель главы семьи (App Router, TanStack Query, shadcn/ui). Перед правками читай `web/AGENTS.md`: API Next 16 отличаются от привычных.
  - `web/src/api/schema.d.ts` генерируется из `openapi.yaml` (`npm run gen:api`), руками не править.
  - Фильтры живут в URL (`use-filters.ts`); `/api/*` проксируется на Go API через rewrites в `next.config.ts`.

## Команды

```bash
make up          # docker compose up -d --build
make gen         # перегенерировать код из openapi.yaml
make test        # все тесты (интеграционные поднимают Postgres через testcontainers)
make test-short  # только юнит-тесты
make lint        # go vet + golangci-lint в Docker
make eval        # eval агента на GigaChat (RUNS=3 для оценки стабильности)

# web/
npm run dev        # панель на :3000, API ожидается на :8080
npm run gen:api    # TS-типы из backend/api/openapi.yaml
npm run typecheck && npm run lint
```

## Правила

- **Contract-first.** Любое изменение REST API начинается с `backend/api/openapi.yaml`, затем `make gen` и `npm run gen:api` в `web/`. Сгенерированный код руками не править.
- **Scope обязателен.** Все операции с тратами принимают `expense.Scope`: `AllUsers()` для панели, `OwnedBy(telegramID)` для бота. Нулевой scope не видит ничего — не обходить это.
- **user_id для бота берётся только из Telegram-апдейта**, никогда из аргументов LLM.
- **Деньги — `decimal.Decimal` / `NUMERIC(12,2)`**, никаких float.
- **Даты трат — календарные** (`time.Time` в полночь UTC), «сегодня» считается в `APP_TIMEZONE`.
- Доменные ошибки (`ValidationError`, `ErrNotFound`, `ErrUnknownUser`) превращаются в HTTP-ответы только в `httpapi/errors.go`.
- Комментарии и сообщения в коде — на английском, документация для людей — на русском.
- После изменений: `make lint && make test`. После правок промпта, тулов или модели — `make eval` и сравнить с прошлым `report.md`.
