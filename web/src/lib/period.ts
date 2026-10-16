import { toISODate } from "./format";

export const PERIODS = {
  "this-month": "Этот месяц",
  "last-month": "Прошлый месяц",
  "this-week": "Эта неделя",
  "last-30": "Последние 30 дней",
  "this-year": "Этот год",
  all: "Всё время",
  custom: "Свой период",
} as const;

export type Period = keyof typeof PERIODS;

export const DEFAULT_PERIOD: Period = "this-month";

export function isPeriod(s: string | null): s is Period {
  return s !== null && s in PERIODS;
}

/**
 * Chart step for a date range: days up to two months, then weeks up to half a
 * year, then months. Without a start ("all time") months keep the chart readable.
 */
export function bucketFor(from?: string, to?: string): "day" | "week" | "month" {
  if (!from) return "month";
  const days =
    (new Date(to ?? toISODate(new Date())).getTime() - new Date(from).getTime()) / 86_400_000;
  if (days <= 62) return "day";
  if (days <= 183) return "week";
  return "month";
}

/** Date range of a preset period, inclusive, as "YYYY-MM-DD" strings. */
export function periodRange(period: Exclude<Period, "custom">, now = new Date()) {
  const y = now.getFullYear();
  const m = now.getMonth();
  const today = toISODate(now);
  switch (period) {
    case "this-month":
      return { from: toISODate(new Date(y, m, 1)), to: today };
    case "last-month":
      return { from: toISODate(new Date(y, m - 1, 1)), to: toISODate(new Date(y, m, 0)) };
    case "this-week": {
      const monday = new Date(y, m, now.getDate() - ((now.getDay() + 6) % 7));
      return { from: toISODate(monday), to: today };
    }
    case "last-30":
      return { from: toISODate(new Date(y, m, now.getDate() - 29)), to: today };
    case "this-year":
      return { from: toISODate(new Date(y, 0, 1)), to: today };
    case "all":
      return { from: undefined, to: undefined };
  }
}
