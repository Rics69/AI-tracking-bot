-- Demo data for the web panel: three family members, a month of expenses and their salaries.
-- Lets you look at the panel without talking to the bot first:
--   make seed
-- The panel shows the family of ADMIN_TELEGRAM_ID (passed as :admin), so the
-- demo members join it. Telegram IDs 1001–1003 are fake.
-- Dates are relative to today, so the data always looks fresh. Safe to re-run:
-- previous demo data is replaced.

BEGIN;

DELETE FROM users WHERE telegram_id IN (1001, 1002, 1003); -- cascades to their operations

-- The admin may not have written to the bot yet: create them as a family head.
SELECT NOT EXISTS (SELECT 1 FROM users WHERE telegram_id = :admin) AS admin_missing \gset
\if :admin_missing
WITH f AS (INSERT INTO families DEFAULT VALUES RETURNING id)
INSERT INTO users (telegram_id, family_id, first_name, is_head) SELECT :admin, id, 'Глава семьи', true FROM f;
\endif

INSERT INTO users (telegram_id, family_id, first_name, username, consented_at)
SELECT v.id, admin.family_id, v.first_name, v.username, now()
FROM users admin, (VALUES
    (1001, 'Анна',  'anna_demo'),
    (1002, 'Борис', NULL),
    (1003, 'Вера',  'vera_demo')) AS v (id, first_name, username)
WHERE admin.telegram_id = :admin;

INSERT INTO operations (user_id, kind, amount, category, occurred_on, comment) VALUES
    (1001, 'expense',   350.00, 'food',          CURRENT_DATE,      'кофе'),
    (1001, 'expense',  2480.50, 'food',          CURRENT_DATE - 1,  'продукты'),
    (1001, 'expense',  1200.00, 'transport',     CURRENT_DATE - 1,  'такси'),
    (1001, 'expense',   640.00, 'health',        CURRENT_DATE - 3,  'аптека'),
    (1001, 'expense', 45000.00, 'housing',       CURRENT_DATE - 5,  'аренда квартиры'),
    (1001, 'expense',   890.00, 'entertainment', CURRENT_DATE - 9,  'кино'),
    (1001, 'expense',  3100.00, 'food',          CURRENT_DATE - 12, 'продукты на неделю'),
    (1002, 'expense',  3000.00, 'transport',     CURRENT_DATE,      'бензин'),
    (1002, 'expense',   450.00, 'food',          CURRENT_DATE - 2,  'обед'),
    (1002, 'expense',  7490.00, 'shopping',      CURRENT_DATE - 4,  'кроссовки'),
    (1002, 'expense',   299.00, 'entertainment', CURRENT_DATE - 6,  'подписка на кинотеатр'),
    (1002, 'expense',  5600.00, 'housing',       CURRENT_DATE - 8,  'коммуналка'),
    (1002, 'expense',  1500.00, 'other',         CURRENT_DATE - 15, 'подарок коллеге'),
    (1003, 'expense',   120.00, 'food',          CURRENT_DATE,      'булка'),
    (1003, 'expense',  2100.00, 'health',        CURRENT_DATE - 2,  'стоматолог'),
    (1003, 'expense',  1350.00, 'shopping',      CURRENT_DATE - 7,  'книги'),
    (1003, 'expense',   180.00, 'transport',     CURRENT_DATE - 10, 'метро'),
    (1003, 'expense',  2750.00, 'entertainment', CURRENT_DATE - 20, 'концерт'),
    (1003, 'expense',   980.00, 'food',          CURRENT_DATE - 35, 'кафе с подругой');

-- Incomes are not in the panel yet, but the bot and weekly reports see them.
INSERT INTO operations (user_id, kind, amount, category, occurred_on, comment) VALUES
    (1001, 'income', 85000.00, 'salary',    CURRENT_DATE - 14, 'зарплата'),
    (1002, 'income', 62000.00, 'salary',    CURRENT_DATE - 14, 'зарплата'),
    (1002, 'income',  7500.00, 'freelance', CURRENT_DATE - 3,  'заказ на сайт'),
    (1003, 'income',   430.00, 'interest',  CURRENT_DATE - 1,  'кэшбэк');

COMMIT;
