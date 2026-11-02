"use client";

import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import type { NetWorthPoint } from "@/api/client";
import { ChartContainer, ChartTooltip, type ChartConfig } from "@/components/ui/chart";
import { capitalize } from "@/lib/categories";
import { formatMoney, parseDate } from "@/lib/format";

// One series: the line wears the ink colour and the title names it.
const config = { net: { label: "Чистый капитал", color: "var(--foreground)" } } satisfies ChartConfig;

const monthShort = new Intl.DateTimeFormat("ru-RU", { month: "short" });
const monthLong = new Intl.DateTimeFormat("ru-RU", { month: "long", year: "numeric" });
const compact = new Intl.NumberFormat("ru-RU", { notation: "compact", maximumFractionDigits: 1 });

export function signedMoney(amount: number): string {
  return (amount < 0 ? "−" : "") + formatMoney(Math.abs(amount));
}

export function monthLabel(s: string): string {
  return capitalize(monthLong.format(parseDate(s)));
}

/** Net worth month by month. */
export function NetWorthChart({ points }: { points: NetWorthPoint[] }) {
  return (
    <ChartContainer config={config} className="aspect-auto h-44 w-full">
      <AreaChart data={points} margin={{ left: 0, right: 4, top: 8 }}>
        <defs>
          <linearGradient id="net-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--foreground)" stopOpacity={0.12} />
            <stop offset="100%" stopColor="var(--foreground)" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} strokeDasharray="3 4" />
        <XAxis
          dataKey="month"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={24}
          tickFormatter={(v: string) => monthShort.format(parseDate(v))}
        />
        <YAxis tickLine={false} axisLine={false} width={60} tickFormatter={(v: number) => compact.format(v)} />
        <ChartTooltip cursor={{ stroke: "var(--foreground)", strokeOpacity: 0.25, strokeWidth: 1 }} content={<Tooltip />} />
        <Area
          dataKey="net"
          type="monotone"
          baseValue="dataMin"
          stroke="var(--foreground)"
          strokeWidth={2}
          fill="url(#net-fill)"
          activeDot={{ r: 4, stroke: "var(--card)", strokeWidth: 2 }}
          isAnimationActive={false}
        />
      </AreaChart>
    </ChartContainer>
  );
}

function Tooltip({ active, payload }: { active?: boolean; payload?: { payload: NetWorthPoint }[] }) {
  const row = payload?.[0]?.payload;
  if (!active || !row) return null;
  return (
    <div className="grid min-w-32 gap-1 rounded-xl border border-border bg-popover px-3 py-2 text-[13px] text-popover-foreground shadow-md">
      <span className="font-medium">{monthLabel(row.month)}</span>
      <span className="tabular-nums">{signedMoney(row.net)}</span>
    </div>
  );
}
