"use client";

import { BarChart3Icon, TableIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";

import type { Bucket, Category, ExpenseCategory, TimePoint } from "@/api/client";
import type { ExpenseQuery } from "@/api/queries";
import { useTimeSeries } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ChartContainer, ChartLegend, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatMoney, parseDate } from "@/lib/format";
import { bucketFor } from "@/lib/period";

import { capitalize } from "./filters-bar";

// Each category keeps its colour whatever the filter: colour follows the
// entity, never its rank. Hues are the validated categorical palette in its
// fixed order (light / dark steps), checked for colour-blind separation.
const CATEGORY_ORDER: ExpenseCategory[] = [
  "food",
  "transport",
  "housing",
  "entertainment",
  "health",
  "shopping",
  "other",
];
const PALETTE: Record<ExpenseCategory, { light: string; dark: string }> = {
  food: { light: "#2a78d6", dark: "#3987e5" },
  transport: { light: "#eb6834", dark: "#d95926" },
  housing: { light: "#1baf7a", dark: "#199e70" },
  entertainment: { light: "#eda100", dark: "#c98500" },
  health: { light: "#e87ba4", dark: "#d55181" },
  shopping: { light: "#008300", dark: "#008300" },
  other: { light: "#6250d6", dark: "#9085e9" },
};

const BUCKET_TITLE: Record<Bucket, string> = {
  day: "по дням",
  week: "по неделям",
  month: "по месяцам",
};

type Row = { start: string; total: number; count: number } & Partial<Record<Category, number>>;

type Props = {
  query: ExpenseQuery;
  categoryLabel: (code: string) => string;
};

export function SpendingChart({ query, categoryLabel }: Props) {
  const bucket = bucketFor(query.from, query.to);
  const series = useTimeSeries(query, bucket);
  const [asTable, setAsTable] = useState(false);

  const { rows, present, config } = useMemo(() => {
    const items = series.data?.items ?? [];
    const present = CATEGORY_ORDER.filter((c) =>
      items.some((p) => p.categories.some((t) => t.category === c && t.sum > 0)),
    );
    const config: ChartConfig = Object.fromEntries(
      present.map((c) => [c, { label: capitalize(categoryLabel(c)), theme: PALETTE[c] }]),
    );
    return { rows: items.map(toRow), present, config };
  }, [series.data, categoryLabel]);

  const empty = rows.every((r) => r.total === 0);

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-sm font-medium text-muted-foreground">
          Траты {BUCKET_TITLE[bucket]}, ₽
        </CardTitle>
        <CardAction>
          <Button
            variant="ghost"
            size="sm"
            aria-pressed={asTable}
            onClick={() => setAsTable((v) => !v)}
          >
            {asTable ? <BarChart3Icon /> : <TableIcon />}
            {asTable ? "График" : "Таблица"}
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {!series.data ? (
          <Skeleton className="h-64 w-full" />
        ) : empty ? (
          <p className="py-16 text-center text-sm text-muted-foreground">
            Нет трат за выбранный период
          </p>
        ) : asTable ? (
          <SeriesTable rows={rows} bucket={bucket} categoryLabel={categoryLabel} />
        ) : (
          <ChartContainer config={config} className="aspect-auto h-64 w-full">
            <BarChart data={rows} margin={{ left: 4, right: 4, top: 8 }} barCategoryGap="20%">
              <CartesianGrid vertical={false} strokeDasharray="0" />
              <XAxis
                dataKey="start"
                tickLine={false}
                axisLine={false}
                tickMargin={8}
                minTickGap={24}
                tickFormatter={(v: string) => tickLabel(v, bucket)}
              />
              <YAxis
                tickLine={false}
                axisLine={false}
                width={56}
                tickFormatter={(v: number) => compactMoney(v)}
              />
              <ChartTooltip
                cursor={{ fill: "var(--muted)", opacity: 0.6 }}
                content={<SeriesTooltip bucket={bucket} order={present} config={config} />}
              />
              {present.length > 1 && (
                <ChartLegend
                  verticalAlign="bottom"
                  content={<SeriesLegend order={present} config={config} />}
                />
              )}
              {present.map((c, i) => (
                <Bar
                  key={c}
                  dataKey={c}
                  stackId="spending"
                  fill={`var(--color-${c})`}
                  // A 2px surface-coloured edge separates stacked segments.
                  stroke="var(--card)"
                  strokeWidth={2}
                  isAnimationActive={false}
                  shape={(props: BarShapeProps) => (
                    <RoundedTopBar {...props} isTop={isTopSegment(props.payload, present, i)} />
                  )}
                />
              ))}
            </BarChart>
          </ChartContainer>
        )}
      </CardContent>
    </Card>
  );
}

function toRow(p: TimePoint): Row {
  const row: Row = { start: p.start, total: p.sum, count: p.count };
  for (const c of p.categories) row[c.category] = c.sum;
  return row;
}

type BarShapeProps = {
  x?: number;
  y?: number;
  width?: number;
  height?: number;
  fill?: string;
  stroke?: string;
  strokeWidth?: number | string;
  payload?: Row;
};

/** The highest non-empty segment of a stack gets the rounded data end. */
function isTopSegment(row: Row | undefined, order: Category[], index: number): boolean {
  if (!row) return false;
  return order.slice(index + 1).every((c) => !row[c]);
}

function RoundedTopBar({
  x = 0,
  y = 0,
  width = 0,
  height = 0,
  isTop,
  ...rest
}: BarShapeProps & { isTop: boolean }) {
  if (height <= 0 || width <= 0) return null;
  const r = isTop ? Math.min(4, width / 2, height) : 0;
  const d = `M${x},${y + height} V${y + r} Q${x},${y} ${x + r},${y} H${x + width - r} Q${x + width},${y} ${x + width},${y + r} V${y + height} Z`;
  return <path d={d} fill={rest.fill} stroke={rest.stroke} strokeWidth={rest.strokeWidth} />;
}

type TooltipProps = {
  active?: boolean;
  payload?: { payload: Row }[];
  bucket: Bucket;
  order: Category[];
  config: ChartConfig;
};

function SeriesTooltip({ active, payload, bucket, order, config }: TooltipProps) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  const parts = order.filter((c) => row[c]).sort((a, b) => (row[b] ?? 0) - (row[a] ?? 0));
  return (
    <div className="grid min-w-44 gap-2 rounded-lg bg-background px-3 py-2 text-xs shadow-md ring-1 ring-foreground/10">
      <div className="flex items-baseline justify-between gap-4">
        <span className="font-medium">{rangeLabel(row.start, bucket)}</span>
        <span className="font-medium tabular-nums">{formatMoney(row.total)}</span>
      </div>
      {parts.length > 0 && (
        <ul className="grid gap-1">
          {parts.map((c) => (
            <li key={c} className="flex items-center gap-2">
              <span
                className="size-2.5 shrink-0 rounded-sm"
                style={{ background: `var(--color-${c})` }}
              />
              <span className="text-muted-foreground">{config[c]?.label}</span>
              <span className="ml-auto tabular-nums">{formatMoney(row[c] ?? 0)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function SeriesLegend({ order, config }: { order: Category[]; config: ChartConfig }) {
  return (
    <ul className="flex flex-wrap justify-center gap-x-4 gap-y-1 pt-4 text-xs text-muted-foreground">
      {order.map((c) => (
        <li key={c} className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm" style={{ background: `var(--color-${c})` }} />
          {config[c]?.label}
        </li>
      ))}
    </ul>
  );
}

/** The same numbers as a table: identity never depends on colour alone. */
function SeriesTable({
  rows,
  bucket,
  categoryLabel,
}: {
  rows: Row[];
  bucket: Bucket;
  categoryLabel: (code: string) => string;
}) {
  return (
    <div className="max-h-72 overflow-y-auto">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Период</TableHead>
            <TableHead className="text-right">Сумма</TableHead>
            <TableHead className="text-right">Трат</TableHead>
            <TableHead>Больше всего</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows
            .filter((r) => r.total > 0)
            .map((r) => {
              const top = CATEGORY_ORDER.filter((c) => r[c]).sort(
                (a, b) => (r[b] ?? 0) - (r[a] ?? 0),
              )[0];
              return (
                <TableRow key={r.start}>
                  <TableCell>{rangeLabel(r.start, bucket)}</TableCell>
                  <TableCell className="text-right tabular-nums">{formatMoney(r.total)}</TableCell>
                  <TableCell className="text-right tabular-nums">{r.count}</TableCell>
                  <TableCell className="text-muted-foreground">
                    {top ? `${categoryLabel(top)} — ${formatMoney(r[top] ?? 0)}` : "—"}
                  </TableCell>
                </TableRow>
              );
            })}
        </TableBody>
      </Table>
    </div>
  );
}

const dayFormat = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" });
const monthFormat = new Intl.DateTimeFormat("ru-RU", { month: "short", year: "numeric" });
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

/** Axis ticks: "12 тыс." instead of "12 000 ₽"; the unit is in the title. */
function compactMoney(v: number): string {
  return compact.format(v);
}
