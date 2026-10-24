# Запуск «Сейвпоинта» на сервере

Всё работает на одном VPS в Docker: Caddy (HTTPS), mini app, API, бот, PostgreSQL и ночные бэкапы — [`docker-compose.prod.yml`](../docker-compose.prod.yml). Наружу открыты только порты 80 и 443.

## 1. Что подготовить

- **VPS в России** — данные пользователей по 152-ФЗ должны храниться в РФ. Подойдёт любой облачный хостинг с Ubuntu 24.04: от 2 vCPU и 2 ГБ памяти (сборка mini app на 1 ГБ не уместится), 20 ГБ диска.
- **Домен** и A-запись на IP сервера (например, `savepoint.ru` или `app.savepoint.ru`). Caddy сам получит сертификат Let's Encrypt, когда домен начнёт указывать на сервер.
- **Боевой бот** в @BotFather — отдельный от dev-бота, его токен живёт только на сервере.
- **Ключ GigaChat API.** Тариф `GIGACHAT_API_PERS` — для физлиц; для публичного сервиса уточните условия (см. «Юридическое»).
- **Объектное хранилище** для копий бэкапов (Yandex Object Storage, Selectel S3 и т. п.) — необязательно, но без него бэкапы лежат на том же сервере.

## 2. Сервер

Под root или пользователем с sudo:

```bash
curl -fsSL https://get.docker.com | sh
```

```bash
ufw allow OpenSSH && ufw allow 80 && ufw allow 443 && ufw --force enable
```

Код — клон репозитория, ветка `savepoint`. Если репозиторий приватный, добавьте серверу deploy key в настройках GitHub.

```bash
git clone -b savepoint https://github.com/Rics69/AI-tracking-bot.git savepoint
```

## 3. `.env` на сервере

```bash
cd savepoint && cp .env.example .env && nano .env
```

| Переменная | Что вписать |
|---|---|
| `DOMAIN` | домен без https://, например `savepoint.ru` |
| `TELEGRAM_BOT_TOKEN` | токен **боевого** бота |
| `ADMIN_TELEGRAM_ID` | ваш Telegram ID: `/stats`, `/ban`, отзывы, оповещения о сбоях GigaChat |
| `GIGACHAT_AUTH_KEY` | ключ GigaChat API |
| `POSTGRES_PASSWORD` | длинный случайный пароль: `openssl rand -hex 24` |
| `S3_*` | хранилище для копий бэкапов, если есть |

`MINI_APP_URL` и `PRIVACY_URL` можно не задавать: они собираются из `DOMAIN` (`https://<домен>` и `https://<домен>/privacy`). `APP_ENV` и `AUTH_DEV_USER_ID` в проде не используются — вход только через Telegram.

## 4. Первый запуск

```bash
docker compose -f docker-compose.prod.yml up -d --build
```

Проверьте:
- `https://<домен>/privacy` открывается в браузере (политика из [`docs/privacy.md`](privacy.md));
- `https://<домен>` в браузере показывает «Откройте из Telegram» — так и должно быть;
- бот отвечает на `/start`, кнопка «Открыть» слева от поля ввода открывает приложение;
- `/stats` от вашего аккаунта показывает статистику.

Логи:

```bash
docker compose -f docker-compose.prod.yml logs -f --since 30m bot api caddy
```

## 5. Обновления

Локально, когда всё закоммичено и запушено:

```bash
make deploy DEPLOY_HOST=root@<ip-сервера>
```

`make deploy` откажется, если в ветке есть незапушенные коммиты. На сервере он делает `git pull` и пересобирает контейнеры; миграции базы применяются при старте. Политику (`docs/privacy.md`) Caddy читает с диска — правка видна сразу после `git pull`, без пересборки.

Откат — `git checkout <коммит>` на сервере и та же команда `docker compose … up -d --build`. Миграции назад сами не откатываются: если релиз менял схему базы, сначала восстановите бэкап.

## 6. Бэкапы

Контейнер `backup` каждую ночь в 03:00 по Москве (`BACKUP_HOUR_UTC=0`) делает `pg_dump` в `./backups` на сервере, хранит 14 дней (`BACKUP_KEEP_DAYS`) и, если задан `S3_BUCKET`, копирует дамп в хранилище.

Бэкап прямо сейчас:

```bash
docker compose -f docker-compose.prod.yml exec backup backup.sh now
```

**Восстановление** — сначала в отдельную базу, чтобы посмотреть, что там:

```bash
docker compose -f docker-compose.prod.yml exec postgres createdb -U expenses restore_check
```

```bash
gunzip -c backups/expenses-2026-10-24_0000.sql.gz | docker compose -f docker-compose.prod.yml exec -T postgres psql -v ON_ERROR_STOP=1 -U expenses -d restore_check
```

Если всё на месте и нужно заменить рабочую базу: остановите `bot` и `api`, удалите и создайте заново базу `expenses` и восстановите в неё тем же `gunzip | psql`, затем снова `up -d`.

Скрипт бэкапа уже проверен восстановлением на чистую базу: все таблицы и суммы совпали. После запуска один раз повторите проверку на сервере.

## 7. BotFather

В @BotFather для боевого бота:
- `/setname` — «Сейвпоинт»;
- `/setdescription` — что делает бот (видно до первого `/start`);
- `/setabouttext` — одна строка в профиле бота;
- `/setuserpic` — аватар (Сейвик);
- `/setjoingroups` → Disable: бот работает только в личных сообщениях;
- Bot Settings → Configure Mini App → Enable, URL `https://<домен>` — кнопка «Открыть приложение» в профиле бота и ссылки `t.me/<бот>?startapp`.

Меню команд и кнопку «Открыть» слева от поля ввода бот ставит сам при старте; у вас в меню дополнительно `/stats`, `/ban`, `/unban`.

## 8. Юридическое (не консультация — проверьте сами)

- Заполните всё в `[квадратных скобках]` в [`docs/privacy.md`](privacy.md): кто оператор, контакты, хостинг, срок журналов, дата редакции.
- Статус оператора персональных данных: самозанятый или ИП.
- Уведомление Роскомнадзора об обработке персональных данных (pd.rkn.gov.ru).
- Условия GigaChat API для публичного продукта: возможно, нужен договор для юрлиц/ИП вместо тарифа для физлиц.

## 9. Самопроверка перед запуском

Вдвоём, со вторым аккаунтом Telegram, на боевом боте:
- новый пользователь: `/start` → согласие → приветствие;
- расход и доход текстом, правка, удаление;
- семья: вступить по ссылке с уже записанными тратами, выйти, исключить; чужие траты не видны, глава не может менять чужое;
- дневной лимит сообщений (временно `USER_DAILY_MESSAGES=3` на втором аккаунте);
- лимит, цель, регулярный платёж в боте и в приложении;
- `/delete_me` на втором аккаунте;
- mini app на iOS, Android и Telegram Desktop, светлая и тёмная тема;
- утреннее напоминание (поставьте час в «Цели и лимит») и итоги недели;
- `/stats`, `/ban` и `/unban` второго аккаунта;
- бэкап вручную и восстановление в `restore_check`.
