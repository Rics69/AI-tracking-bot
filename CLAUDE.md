# CLAUDE.md

Сервис учёта расходов «Сейвпоинт»: Telegram-бот с ИИ-агентом (GigaChat) + mini app в Telegram. Бот открыт всем, каждый пользователь живёт в своей семье (сначала из одного человека).
ТЗ — `test-task-expenses.md`.

## Структура

- `backend/` — Go-модуль, бинари в `cmd/`:
  - `cmd/api` — REST API для mini app.
  - `cmd/bot` — Telegram-бот с агентом (long polling).
  - `cmd/chat` — консольный чат с агентом без Telegram, для отладки промпта.
  - `cmd/eval` — eval агента на настоящем LLM: сценарии в `backend/eval/cases.yaml`, каждый на чистом in-memory хранилище.
- `backend/internal/ledger` — домен денег: операции — траты и доходы (вид определяется категорией, `Category.Kind()`), валидация, `Service`. Без SQL и HTTP. В БД — таблица `operations`.
- `backend/internal/llm` — интерфейс модели, не зависящий от провайдера; `llm/gigachat` — клиент GigaChat (OAuth, сертификат Минцифры, function calling); `llm/provider` — выбор провайдера по `LLM_PROVIDER`.
- `backend/internal/agent` — агент: тулы над `ledger.Service` (`add_expense`, `add_income`, `find_operations`, `get_summary`, `update_operation`, `delete_operation`) и, если в `Config.Budget` передан `budget.Service`, над лимитом (`get_budget`, `set_payday`, `add_regular_payment`, `set_goal`, `deposit_to_goal`; `budget_tools.go`), системный промпт (цели и платежи пользователя — в конце, чтобы начало кэшировалось), цикл tool calling. История диалога — интерфейс `HistoryStore`: в боте PostgreSQL (`postgres.ChatHistoryRepository`, ход целиком в JSONB), в eval и `cmd/chat` — память.
- `backend/internal/telegram` — обработчик апдейтов и кнопок: согласие на обработку данных, команды (`/family`, `/settings`, `/privacy`, `/feedback`, `/delete_me`), дневной лимит, очередь по пользователю, «печатает…».
- `backend/internal/texts` — все тексты бота в `texts.yaml` (вшит в бинарь, проверяется при старте). Тексты меняются там, не в коде.
- `backend/internal/user` — пользователи: `family_id`, роль главы, часовой пояс, согласие, блокировка.
- `backend/internal/family` — семьи: новый пользователь получает свою семью, вступление по приглашению (в БД только хэш кода), выход, исключение, удаление аккаунта (семья главы распускается).
- `backend/internal/usage` — дневной учёт токенов LLM на пользователя (`llm_usage`) и лимиты; `ADMIN_TELEGRAM_ID` без лимитов.
- `backend/internal/storage/postgres` — репозитории на pgx, миграции goose (вшиты через embed, применяются при старте).
- `backend/internal/storage/memory` — in-memory репозитории трат и бюджета с той же семантикой (для eval и тестов).
- `backend/internal/budget` — необязательная часть: цели (`goals`, пополнения в `goal_deposits`, минус — забрать), план (`budget_plans`: 1–2 дня зарплаты, ожидаемый доход, взнос в цели, час напоминания), регулярные платежи (`regular_payments`; трата ссылается на платёж через `operations.regular_payment_id`). `Compute` считает лимит дня кодом, ничего не хранится. Всё личное: только `user_id`, без семейного scope.
- `backend/internal/stats` — `/stats` для владельца: только количества по всей базе (`postgres.StatsRepository`), текст собирается кодом. Команды владельца (`/stats`, `/ban`, `/unban`) — `telegram/admin.go`, видны в меню только в чате `ADMIN_TELEGRAM_ID`. `usage.Meter.OnOutage` шлёт владельцу оповещение, если 5 ответов агента подряд упали (не чаще раза в час).
- `backend/internal/report` — сообщения, которые бот шлёт сам, собираются кодом (не LLM): итоги недели (понедельник 10:00) и утреннее напоминание о лимите и завтрашних платежах (`daily.go`, час из плана, фразы — `reminders` в `texts.yaml`). `notification_runs` (`kind`, `period_key`, `user_id`) гарантирует одну отправку. Постоянные ошибки Telegram (`telegram.ErrUndeliverable`) не ретраятся.
- `backend/internal/httpapi` — REST для mini app: `api.gen.go` генерируется из `backend/api/openapi.yaml`, руками пишутся `server.go`, `family.go`, `budget.go`, `auth.go` (вход по `initData`).
- `backend/internal/initdata` — проверка подписи Telegram Mini App `initData`.
- `backend/internal/savik` — состояние маскота Сейвика (считается кодом, по порядку): `full` (цель достигнута за 7 дней), `empty` (до зарплаты ничего не осталось; без плана — траты месяца больше доходов), `hungry` (сегодня сверх лимита), `fed` (цель на 80%), `neutral`.
- `web/` — Next.js 16 mini app для Telegram (App Router, TanStack Query, shadcn/ui, motion). Перед правками читай `web/AGENTS.md`: API Next 16 отличаются от привычных.
  - Экраны: `/` главная (с лимитом и целями), `/history`, `/analytics`, `/profile`, `/plan` — «Цели и лимит» (`components/budget/`: при первом входе мастер из трёх пропускаемых шагов, потом обзор); нижнее меню и общая шторка «＋» (`operations/sheet-context.tsx`). Всё за `shell/gate.tsx`: вне Telegram — экран «откройте из Telegram», без согласия — экран согласия.
  - Данные пользователя грузятся только в браузере: страницы с `export const instant = false`, никакого `Math.random()`/`Date` при пререндере (фразы Сейвика выбираются через `useSyncExternalStore`).
  - Дизайн — в духе Cadra: молочный фон, белые карточки, чёрная «hero»-карточка, пастельно-голубая «soft», плавающая капсула меню. Токены (`--hero`, `--soft`, `--nav`, `--success` …) в `globals.css`, светлая и тёмная тема своя; из Telegram берётся только `colorScheme` (`shell/telegram-setup.tsx` красит под фон шапку и низ). Шрифт Onest — локально (`@fontsource-variable/onest`), без Google Fonts. Общие блоки — `components/kit.tsx` (`Card`, `Panel`, `StatCard`, `Segmented`, `Pill`, `PillButton`, `PageHeader`). `lib/telegram.ts` — обёртка над `telegram-web-app.js` (хаптик, BackButton, share, confirm).
  - Тексты и фразы Сейвика — `src/content/` (правит владелец), позы — `src/content/savik.ts`.
  - `web/src/api/schema.d.ts` генерируется из `openapi.yaml` (`npm run gen:api`), руками не править.
  - `/api/*` проксируется на Go API через rewrites в `next.config.ts`.
  - Позы Сейвика: исходники в `poses/*.png` (не в git), `npm run poses` собирает `web/public/savik/{512,256}/*.webp`. Руками webp не править.
  - Графики (`components/money/`): у категорий своя проверенная палитра из 7 цветов (`distribution.tsx`, светлая и тёмная), цвет закреплён за категорией, рядом всегда подпись и сумма. Динамика (`flow-chart.tsx`) — одна серия цветом текста, без категориальных цветов. Новые цвета — только после проверки валидатором.

## Продакшен

- `docker-compose.prod.yml` — один VPS: Caddy (`deploy/Caddyfile`, HTTPS по `DOMAIN`), `backup` (`deploy/backup/backup.sh`: ночной `pg_dump`, 14 дней, опционально S3), наружу только 80/443, `APP_ENV=prod`. Пошагово — `docs/deploy.md`.
- `docs/privacy.md` — политика данных (правит владелец), Caddy отдаёт её на `/privacy` через шаблон `deploy/privacy.html`; правки видны без пересборки.
- С российского сервера `api.telegram.org` часто недоступен: `TELEGRAM_PROXY` пускает через прокси только клиент Telegram (GigaChat — напрямую); в проде это сервис `telegram-proxy` (профиль compose, SSH-туннель за рубеж, ключ в `deploy/telegram-proxy/keys/`, не в git).
- `make deploy DEPLOY_HOST=user@host` — `git pull` и `up -d --build` на сервере; откажется при незапушенных коммитах.
- CI — `.github/workflows/ci.yml`: vet, golangci-lint, тесты, typecheck, lint, сборка web, сверка сгенерированного кода.

## Команды

```bash
make up          # docker compose up -d --build
make gen         # перегенерировать код из openapi.yaml
make test        # все тесты (интеграционные поднимают Postgres через testcontainers)
make test-short  # только юнит-тесты
make lint        # go vet + golangci-lint в Docker
make eval        # eval агента на GigaChat (RUNS=3 для оценки стабильности)

# web/
npm run dev        # mini app на :3000, API ожидается на :8080
npm run gen:api    # TS-типы из backend/api/openapi.yaml
npm run poses      # позы Сейвика из poses/*.png
npm run typecheck && npm run lint
```

## Правила

- **Contract-first.** Любое изменение REST API начинается с `backend/api/openapi.yaml`, затем `make gen` и `npm run gen:api` в `web/`. Сгенерированный код руками не править.
- **Scope обязателен.** Все операции с тратами принимают `ledger.Scope`: `InFamily(familyID)` — чтение для главы, `OwnedBy(telegramID)` — бот и любая запись (глава видит семью, но меняет только своё). Scope «все пользователи» не добавлять: в базе много семей. Нулевой scope не видит ничего — не обходить это.
- **user_id для бота берётся только из Telegram-апдейта**, никогда из аргументов LLM.
- **Деньги — `decimal.Decimal` / `NUMERIC(12,2)`**, никаких float.
- **Даты трат — календарные** (`time.Time` в полночь UTC), «сегодня» считается в часовом поясе пользователя (`user.Location`, по умолчанию `APP_TIMEZONE`).
- **Пользователь API — только из `initData`** (`httpapi.Auth`, пакет `initdata`): заголовок `Authorization: tma <initData>`, проверка HMAC токеном бота. `AUTH_DEV_USER_ID` работает только при `APP_ENV=dev` (`config.API.DevUser`). Хендлеры берут пользователя через `caller`/`consented`/`head`, никогда из параметров.
- Доменные ошибки (`ValidationError`, `ErrNotFound`, `ErrUnknownUser`) превращаются в HTTP-ответы только в `httpapi/errors.go`.
- Комментарии и сообщения в коде — на английском, документация для людей — на русском.
- После изменений: `make lint && make test`. После правок промпта, тулов или модели — `make eval` и сравнить с прошлым `report.md`.
