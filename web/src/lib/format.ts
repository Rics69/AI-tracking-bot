const rubles = new Intl.NumberFormat("ru-RU", {
  style: "currency",
  currency: "RUB",
  maximumFractionDigits: 0,
});
const rublesKopecks = new Intl.NumberFormat("ru-RU", {
  style: "currency",
  currency: "RUB",
  minimumFractionDigits: 2,
  maximumFractionDigits: 2,
});

/** 1200 → "1 200 ₽", 1249.9 → "1 249,90 ₽": kopecks only when there are any. */
export function formatMoney(amount: number): string {
  return (Number.isInteger(amount) ? rubles : rublesKopecks).format(amount);
}

const percentFormat = new Intl.NumberFormat("ru-RU", {
  style: "percent",
  maximumFractionDigits: 0,
});

/** 0.372 → "37 %"; a non-zero share that rounds to zero shows as "<1 %". */
export function formatShare(share: number): string {
  return share > 0 && share < 0.005
    ? `<${percentFormat.format(0.01)}`
    : percentFormat.format(share);
}

const dayMonth = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" });
const dayMonthYear = new Intl.DateTimeFormat("ru-RU", {
  day: "numeric",
  month: "short",
  year: "numeric",
});

/** Parses "YYYY-MM-DD" as a local calendar date (new Date(string) would use UTC). */
export function parseDate(s: string): Date {
  const [y, m, d] = s.split("-").map(Number);
  return new Date(y, m - 1, d);
}

/** Formats a Date as "YYYY-MM-DD" in local time. */
export function toISODate(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

/** "2026-10-16" → "16 окт.", with the year only when it is not the current one. */
export function formatDate(s: string, now = new Date()): string {
  const d = parseDate(s);
  return (d.getFullYear() === now.getFullYear() ? dayMonth : dayMonthYear).format(d);
}

const dayMonthTime = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" });

/** An ISO timestamp as a short date: "16 окт.". */
export function formatDateTime(iso: string, now = new Date()): string {
  const d = new Date(iso);
  return (d.getFullYear() === now.getFullYear() ? dayMonthTime : dayMonthYear).format(d);
}

/** Russian plural: plural(3, ["трата", "траты", "трат"]) → "траты". */
export function plural(n: number, forms: [string, string, string]): string {
  const mod10 = n % 10;
  const mod100 = n % 100;
  if (mod10 === 1 && mod100 !== 11) return forms[0];
  if (mod10 >= 2 && mod10 <= 4 && (mod100 < 12 || mod100 > 14)) return forms[1];
  return forms[2];
}
