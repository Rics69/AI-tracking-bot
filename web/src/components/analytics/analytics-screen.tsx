"use client";

import { ChartColumnIcon, ListIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";

import type { Bucket, CategoryTotal, Kind, TimePoint } from "@/api/client";
import { useFamily, useMe, useSummary, useTimeSeries, type OperationQuery } from "@/api/queries";
import { Chips } from "@/components/shell/chips";
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import { Skeleton } from "@/components/ui/skeleton";
import { texts } from "@/content/texts";
import { capitalize, categoryIcons, useCategoryInfo } from "@/lib/categories";
import { formatMoney, formatShare, parseDate } from "@/lib/format";
import { bucketFor, periodRange, PERIODS, type Period } from "@/lib/period";
import { cn } from "@/lib/utils";

type PeriodFilter = Exclude<Period, "custom">;

const periods = (
  ["this-month", "last-month", "this-week", "last-30", "this-year", "all"] as const
).map((p) => ({
  value: p,
  label: PERIODS[p],
}));

// Colour follows the kind everywhere on this screen: slots 1 and 2 of the
// validated categorical palette (checked on light #fff and dark #171717
// surfaces: CVD ΔE ≥ 24, contrast ≥ 3:1). Text never wears these colours.
const KIND_COLORS: Record<Kind, { light: string; dark: string }> = {
  expense: { light: "#2a78d6", dark: "#3987e5" },
  income: { light: "#eb6834", dark: "#d95926" },
};

const chartConfig = {
  expense: { label: "Расходы", theme: KIND_COLORS.expense },
  income: { label: "Доходы", theme: KIND_COLORS.income },
} satisfies ChartConfig;

const BUCKET_TITLE: Record<Bucket, string> = {
  day: "по дням",
  week: "по неделям",
  month: "по месяцам",
};

export function AnalyticsScreen() {
  const me = useMe();
  const family = useFamily();
  const [period, setPeriod] = useState<PeriodFilter>("this-month");
  const query = useMemo<OperationQuery>(() => periodRange(period), [period]);
  const summary = useSummary(query);
  const s = summary.data;
  const empty = s && s.expense.count === 0 && s.income.count === 0;

  return (
    <>
      <ChartColors />
      <h1 className="px-1 text-2xl font-semibold tracking-tight">Аналитика</h1>
      <Chips label="Период" options={periods} value={period} onChange={setPeriod} />
      {me.data?.is_head && (family.data?.members.length ?? 0) > 1 && (
        <p className="-mt-3 px-1 text-[13px] text-muted-foreground">Вся семья</p>
      )}

      <section aria-label="Итоги периода" className="grid grid-cols-3 gap-2">
        <Stat label="Доходы" value={s?.income.sum} />
        <Stat label="Расходы" value={s?.expense.sum} />
        <Stat label="Остаток" value={s?.balance} signed />
      </section>

      {empty ? (
        <p className="rounded-2xl bg-card px-5 py-6 text-center text-[15px] text-muted-foreground">
          {texts.analyticsEmpty}
        </p>
      ) : (
        <>
          <FlowChart query={query} />
          <Breakdown
            title="Расходы по категориям"
            kind="expense"
            items={s?.expense.categories}
            total={s?.expense.sum}
          />
          {(s?.income.count ?? 0) > 0 && (
            <Breakdown
              title="Доходы по категориям"
              kind="income"
              items={s?.income.categories}
              total={s?.income.sum}
            />
          )}
        </>
      )}
    </>
  );
}

function Stat({ label, value, signed }: { label: string; value?: number; signed?: boolean }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 rounded-2xl bg-card px-3 py-3">
      <span className="text-[13px] text-muted-foreground">{label}</span>
      {value === undefined ? (
        <Skeleton className="h-6 w-full" />
      ) : (
        <span
          className={cn(
            "truncate text-base font-semibold tabular-nums",
            signed && value < 0 && "text-destructive",
          )}
        >
          {signed && value < 0 ? "−" : ""}
          {formatMoney(Math.abs(value))}
        </span>
      )}
    </div>
  );
}

type Row = { start: string; expense: number; income: number };

/** Incomes and expenses side by side per day, week or month: one axis, one unit. */
function FlowChart({ query }: { query: OperationQuery }) {
  const bucket = bucketFor(query.from, query.to);
  const expense = useTimeSeries({ ...query, kind: "expense" }, bucket);
  const income = useTimeSeries({ ...query, kind: "income" }, bucket);
  const [asList, setAsList] = useState(false);
  const rows = useMemo(
    () => merge(expense.data?.items, income.data?.items),
    [expense.data, income.data],
  );
  const loading = !expense.data || !income.data;

  return (
    <section className="flex flex-col gap-3 rounded-2xl bg-card p-4">
      <div className="flex items-center justify-between gap-2">
        <h2 className="text-[15px] font-semibold">Доходы и расходы {BUCKET_TITLE[bucket]}, ₽</h2>
        <button
          type="button"
          aria-pressed={asList}
          onClick={() => setAsList((v) => !v)}
          className="flex min-h-11 shrink-0 items-center gap-1.5 rounded-lg px-2 text-[13px] font-medium text-primary"
        >
          {asList ? <ChartColumnIcon className="size-4" /> : <ListIcon className="size-4" />}
          {asList ? "График" : "Списком"}
        </button>
      </div>

      {loading ? (
        <Skeleton className="h-56 w-full" />
      ) : asList ? (
        <RowsTable rows={rows} bucket={bucket} />
      ) : (
        <>
          <ChartContainer config={chartConfig} className="aspect-auto h-56 w-full">
            <BarChart
              data={rows}
              margin={{ left: 0, right: 0, top: 8 }}
              barGap={2}
              barCategoryGap="24%"
            >
              <CartesianGrid vertical={false} strokeDasharray="0" />
              <XAxis
                dataKey="start"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                minTickGap={20}
                tickFormatter={(v: string) => tickLabel(v, bucket)}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={60}
                tickFormatter={(v: number) => compact.format(v)}
              />
              <ChartTooltip
                cursor={{ fill: "var(--muted)", opacity: 0.6 }}
                content={<FlowTooltip bucket={bucket} />}
              />
              {(["expense", "income"] as const).map((k) => (
                <Bar
                  key={k}
                  dataKey={k}
                  fill={`var(--color-${k})`}
                  radius={[4, 4, 0, 0]}
                  maxBarSize={24}
                  isAnimationActive={false}
                />
              ))}
            </BarChart>
          </ChartContainer>
          <Legend />
        </>
      )}
    </section>
  );
}

function merge(expense: TimePoint[] = [], income: TimePoint[] = []): Row[] {
  const rows = new Map<string, Row>();
  for (const p of expense) rows.set(p.start, { start: p.start, expense: p.sum, income: 0 });
  for (const p of income) {
    const r = rows.get(p.start) ?? { start: p.start, expense: 0, income: 0 };
    r.income = p.sum;
    rows.set(p.start, r);
  }
  return [...rows.values()].sort((a, b) => a.start.localeCompare(b.start));
}

function Legend() {
  return (
    <ul className="flex justify-center gap-5 text-[13px] text-muted-foreground">
      {(["expense", "income"] as const).map((k) => (
        <li key={k} className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm" style={{ background: `var(--color-${k})` }} />
          {chartConfig[k].label}
        </li>
      ))}
    </ul>
  );
}

function FlowTooltip({
  active,
  payload,
  bucket,
}: {
  active?: boolean;
  payload?: { payload: Row }[];
  bucket: Bucket;
}) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className="grid min-w-40 gap-1.5 rounded-lg bg-popover px-3 py-2 text-[13px] text-popover-foreground shadow-md ring-1 ring-foreground/10">
      <span className="font-medium">{rangeLabel(row.start, bucket)}</span>
      {(["income", "expense"] as const).map((k) => (
        <span key={k} className="flex items-center gap-2">
          <span
            className="size-2.5 shrink-0 rounded-sm"
            style={{ background: `var(--color-${k})` }}
          />
          <span className="text-muted-foreground">{chartConfig[k].label}</span>
          <span className="ml-auto tabular-nums">{formatMoney(row[k])}</span>
        </span>
      ))}
    </div>
  );
}

/** The chart's numbers as rows: identity never depends on colour alone. */
function RowsTable({ rows, bucket }: { rows: Row[]; bucket: Bucket }) {
  const shown = rows.filter((r) => r.expense > 0 || r.income > 0).reverse();
  return (
    <ul className="flex max-h-72 flex-col divide-y overflow-y-auto text-[15px]">
      {shown.map((r) => (
        <li key={r.start} className="grid grid-cols-[1fr_auto_auto] items-baseline gap-3 py-2">
          <span className="text-muted-foreground">{rangeLabel(r.start, bucket)}</span>
          <span className="tabular-nums">+{formatMoney(r.income)}</span>
          <span className="tabular-nums">−{formatMoney(r.expense)}</span>
        </li>
      ))}
    </ul>
  );
}

/** Categories of one kind as a ranked list with share bars. */
function Breakdown({
  title,
  kind,
  items,
  total,
}: {
  title: string;
  kind: Kind;
  items?: CategoryTotal[];
  total?: number;
}) {
  const categories = useCategoryInfo();
  return (
    <section className="flex flex-col gap-3 rounded-2xl bg-card p-4">
      <h2 className="text-[15px] font-semibold">{title}</h2>
      {!items ? (
        <Skeleton className="h-32 w-full" />
      ) : items.length === 0 ? (
        <p className="text-[15px] text-muted-foreground">Пока ничего</p>
      ) : (
        <ul
          className="flex flex-col gap-3"
          style={{ "--bar": `var(--color-${kind})` } as React.CSSProperties}
        >
          {items.map((t) => {
            const Icon = categoryIcons[t.category];
            const share = total ? t.sum / total : 0;
            return (
              <li key={t.category} className="flex items-center gap-3">
                <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-muted text-muted-foreground">
                  <Icon className="size-[18px]" />
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                  <div className="flex items-baseline justify-between gap-2 text-[15px]">
                    <span className="truncate">{capitalize(categories.label(t.category))}</span>
                    <span className="shrink-0 font-medium tabular-nums">{formatMoney(t.sum)}</span>
                  </div>
                  <div className="flex items-center gap-2">
                    <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-(--bar)"
                        style={{ width: `${Math.max(share * 100, 1)}%` }}
                      />
                    </div>
                    <span className="w-10 shrink-0 text-right text-[13px] text-muted-foreground tabular-nums">
                      {formatShare(share)}
                    </span>
                  </div>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

/** Defines --color-expense / --color-income for the breakdown bars, like ChartContainer does for the chart. */
function ChartColors() {
  return (
    <style>{`
      :root { --color-expense: ${KIND_COLORS.expense.light}; --color-income: ${KIND_COLORS.income.light}; }
      .dark { --color-expense: ${KIND_COLORS.expense.dark}; --color-income: ${KIND_COLORS.income.dark}; }
    `}</style>
  );
}

const dayFormat = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" });
const monthFormat = new Intl.DateTimeFormat("ru-RU", { month: "short" });
const monthLong = new Intl.DateTimeFormat("ru-RU", { month: "long", year: "numeric" });
const compact = new Intl.NumberFormat("ru-RU", { notation: "compact", maximumFractionDigits: 1 });

function tickLabel(start: string, bucket: Bucket): string {
  return (bucket === "month" ? monthFormat : dayFormat).format(parseDate(start));
}

function rangeLabel(start: string, bucket: Bucket): string {
  const d = parseDate(start);
  if (bucket === "day") return dayFormat.format(d);
  if (bucket === "month") return capitalize(monthLong.format(d));
  const end = new Date(d);
  end.setDate(d.getDate() + 6);
  return `${dayFormat.format(d)} – ${dayFormat.format(end)}`;
}
