# CLAUDE.md

Сервис учёта расходов «Сейвпоинт»: Telegram-бот с ИИ-агентом (GigaChat) + веб-панель главы семьи. Бот открыт всем, каждый пользователь живёт в своей семье (сначала из одного человека).
ТЗ — `test-task-expenses.md`.

## Структура

- `backend/` — Go-модуль, бинари в `cmd/`:
  - `cmd/api` — REST API для веб-панели.
  - `cmd/bot` — Telegram-бот с агентом (long polling).
  - `cmd/chat` — консольный чат с агентом без Telegram, для отладки промпта.
  - `cmd/eval` — eval агента на настоящем LLM: сценарии в `backend/eval/cases.yaml`, каждый на чистом in-memory хранилище.
- `backend/internal/ledger` — домен денег: операции — траты и доходы (вид определяется категорией, `Category.Kind()`), валидация, `Service`. Без SQL и HTTP. В БД — таблица `operations`.
- `backend/internal/llm` — интерфейс модели, не зависящий от провайдера; `llm/gigachat` — клиент GigaChat (OAuth, сертификат Минцифры, function calling); `llm/provider` — выбор провайдера по `LLM_PROVIDER`.
- `backend/internal/agent` — агент: тулы над `ledger.Service` (`add_expense`, `add_income`, `find_operations`, `get_summary`, `update_operation`, `delete_operation`), системный промпт, цикл tool calling. История диалога — интерфейс `HistoryStore`: в боте PostgreSQL (`postgres.ChatHistoryRepository`, ход целиком в JSONB), в eval и `cmd/chat` — память.
- `backend/internal/telegram` — обработчик апдейтов и кнопок: согласие на обработку данных, команды (`/family`, `/settings`, `/privacy`, `/feedback`, `/delete_me`), дневной лимит, очередь по пользователю, «печатает…».
- `backend/internal/texts` — все тексты бота в `texts.yaml` (вшит в бинарь, проверяется при старте). Тексты меняются там, не в коде.
- `backend/internal/user` — пользователи: `family_id`, роль главы, часовой пояс, согласие, блокировка.
- `backend/internal/family` — семьи: новый пользователь получает свою семью, вступление по приглашению (в БД только хэш кода), выход, исключение, удаление аккаунта (семья главы распускается).
- `backend/internal/usage` — дневной учёт токенов LLM на пользователя (`llm_usage`) и лимиты; `ADMIN_TELEGRAM_ID` без лимитов.
- `backend/internal/storage/postgres` — репозитории на pgx, миграции goose (вшиты через embed, применяются при старте).
- `backend/internal/storage/memory` — in-memory репозиторий трат с той же семантикой (для eval).
- `backend/internal/report` — итоги недели: собираются кодом (не LLM), отправка по понедельникам 10:00, `report_runs` гарантирует одну отправку. Постоянные ошибки Telegram (`telegram.ErrUndeliverable`) не ретраятся.
- `backend/internal/httpapi` — REST для mini app: `api.gen.go` генерируется из `backend/api/openapi.yaml`, руками пишутся `server.go`, `family.go`, `auth.go` (вход по `initData`).
- `backend/internal/initdata` — проверка подписи Telegram Mini App `initData`.
- `backend/internal/savik` — состояние маскота Сейвика по деньгам месяца (считается кодом).
- `web/` — Next.js 16 панель главы семьи (App Router, TanStack Query, shadcn/ui). Перед правками читай `web/AGENTS.md`: API Next 16 отличаются от привычных.
  - `web/src/api/schema.d.ts` генерируется из `openapi.yaml` (`npm run gen:api`), руками не править.
  - Фильтры живут в URL (`use-filters.ts`); `/api/*` проксируется на Go API через rewrites в `next.config.ts`.
  - Позы Сейвика: исходники в `poses/*.png` (не в git), `npm run poses` собирает `web/public/savik/{512,256}/*.webp`. Руками webp не править.
  - График (`spending-chart.tsx`, Recharts через shadcn chart): цвет закреплён за категорией и берётся из проверенной палитры — не перекрашивать по рангу и не добавлять цвета без проверки.

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
- **Scope обязателен.** Все операции с тратами принимают `ledger.Scope`: `InFamily(familyID)` — чтение для главы, `OwnedBy(telegramID)` — бот и любая запись (глава видит семью, но меняет только своё). Scope «все пользователи» не добавлять: в базе много семей. Нулевой scope не видит ничего — не обходить это.
- **user_id для бота берётся только из Telegram-апдейта**, никогда из аргументов LLM.
- **Деньги — `decimal.Decimal` / `NUMERIC(12,2)`**, никаких float.
- **Даты трат — календарные** (`time.Time` в полночь UTC), «сегодня» считается в часовом поясе пользователя (`user.Location`, по умолчанию `APP_TIMEZONE`).
- **Пользователь API — только из `initData`** (`httpapi.Auth`, пакет `initdata`): заголовок `Authorization: tma <initData>`, проверка HMAC токеном бота. `AUTH_DEV_USER_ID` работает только при `APP_ENV=dev` (`config.API.DevUser`). Хендлеры берут пользователя через `caller`/`consented`/`head`, никогда из параметров.
- Текущая desktop-панель показывает только траты (`kind=expense`); доходы — в mini app (этап 4).
- Доменные ошибки (`ValidationError`, `ErrNotFound`, `ErrUnknownUser`) превращаются в HTTP-ответы только в `httpapi/errors.go`.
- Комментарии и сообщения в коде — на английском, документация для людей — на русском.
- После изменений: `make lint && make test`. После правок промпта, тулов или модели — `make eval` и сравнить с прошлым `report.md`.
