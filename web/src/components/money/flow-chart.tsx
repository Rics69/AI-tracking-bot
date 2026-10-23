"use client";

import { useMemo } from "react";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import type { Bucket, TimePoint } from "@/api/client";
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import { formatMoney, parseDate } from "@/lib/format";
import { capitalize } from "@/lib/categories";

// One series at a time (expenses or incomes, switched above the chart), so the
// line wears the ink colour and needs no legend: the title names it.
const config = { sum: { label: "Сумма", color: "var(--foreground)" } } satisfies ChartConfig;

/** Money per day, week or month: a line with a soft area under it. */
export function FlowChart({ points, bucket }: { points: TimePoint[]; bucket: Bucket }) {
  const rows = useMemo(() => points.map((p) => ({ start: p.start, sum: p.sum, count: p.count })), [points]);
  return (
    <ChartContainer config={config} className="aspect-auto h-52 w-full">
      <AreaChart data={rows} margin={{ left: 0, right: 4, top: 8 }}>
        <defs>
          <linearGradient id="flow-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--foreground)" stopOpacity={0.12} />
            <stop offset="100%" stopColor="var(--foreground)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} strokeDasharray="3 4" />
        <XAxis
          dataKey="start"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={24}
          tickFormatter={(v: string) => tickLabel(v, bucket)}
        />
        <YAxis tickLine={false} axisLine={false} width={60} tickFormatter={(v: number) => compact.format(v)} />
        <ChartTooltip
          cursor={{ stroke: "var(--foreground)", strokeOpacity: 0.25, strokeWidth: 1 }}
          content={<Tooltip bucket={bucket} />}
        />
        <Area
          dataKey="sum"
          type="monotone"
          stroke="var(--foreground)"
          strokeWidth={2}
          fill="url(#flow-fill)"
          activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }}
          isAnimationActive={false}
        />
      </AreaChart>
    </ChartContainer>
  );
}

type Row = { start: string; sum: number; count: number };

function Tooltip({ active, payload, bucket }: { active?: boolean; payload?: { payload: Row }[]; bucket: Bucket }) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className="grid min-w-32 gap-1 rounded-xl border border-border bg-popover px-3 py-2 text-[13px] text-popover-foreground shadow-md">
      <span className="font-medium">{rangeLabel(row.start, bucket)}</span>
      <span className="tabular-nums">{formatMoney(row.sum)}</span>
    </div>
  );
}

const dayFormat = new Intl.DateTimeFormat("ru-RU", { day: "numeric", month: "short" });
const monthFormat = new Intl.DateTimeFormat("ru-RU", { month: "short" });
const monthLong = new Intl.DateTimeFormat("ru-RU", { month: "long", year: "numeric" });
const compact = new Intl.NumberFormat("ru-RU", { notation: "compact", maximumFractionDigits: 1 });

function tickLabel(start: string, bucket: Bucket): string {
  return (bucket === "month" ? monthFormat : dayFormat).format(parseDate(start));
}

export function rangeLabel(start: string, bucket: Bucket): string {
  const d = parseDate(start);
  if (bucket === "day") return dayFormat.format(d);
  if (bucket === "month") return capitalize(monthLong.format(d));
  const end = new Date(d);
  end.setDate(d.getDate() + 6);
  return `${dayFormat.format(d)} – ${dayFormat.format(end)}`;
}
