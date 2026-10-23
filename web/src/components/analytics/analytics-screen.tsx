"use client";

import { ArrowDownLeftIcon, ArrowUpRightIcon, ChartColumnIcon, ChartPieIcon, ListIcon, TrendingUpIcon, WalletIcon } from "lucide-react";
import { useMemo, useState } from "react";

import type { CategoryTotal, Kind } from "@/api/client";
import { useFamily, useMe, useSummary, useTimeSeries, type OperationQuery } from "@/api/queries";
import { Card, PageHeader, Panel, Pill, Segmented, StatCard } from "@/components/kit";
import { CategoryColors, Distribution } from "@/components/money/distribution";
import { FlowChart, rangeLabel } from "@/components/money/flow-chart";
import { Skeleton } from "@/components/ui/skeleton";
import { texts } from "@/content/texts";
import { capitalize, categoryIcons, useCategoryInfo } from "@/lib/categories";
import { formatMoney, formatShare } from "@/lib/format";
import { bucketFor, periodRange, PERIODS, type Period } from "@/lib/period";

type PeriodFilter = Exclude<Period, "custom">;

const periods = (["this-month", "last-month", "this-week", "last-30", "this-year", "all"] as const).map((p) => ({
  value: p,
  label: PERIODS[p],
}));

const BUCKET_TITLE = { day: "по дням", week: "по неделям", month: "по месяцам" } as const;

export function AnalyticsScreen() {
  const me = useMe();
  const family = useFamily();
  const [period, setPeriod] = useState<PeriodFilter>("this-month");
  const query = useMemo<OperationQuery>(() => periodRange(period), [period]);
  const summary = useSummary(query);
  const s = summary.data;
  const empty = s && s.expense.count === 0 && s.income.count === 0;
  const familyWide = me.data?.is_head && (family.data?.members.length ?? 0) > 1;

  return (
    <>
      <CategoryColors />
      <PageHeader title="Аналитика" action={familyWide ? <Pill>вся семья</Pill> : undefined} />
      <Segmented label="Период" options={periods} value={period} onChange={setPeriod} />

      <div className="grid grid-cols-2 gap-3">
        <StatCard tone="soft" icon={ArrowDownLeftIcon} label="Доходы" value={s ? formatMoney(s.income.sum) : <Skeleton className="h-7 w-20" />} />
        <StatCard icon={ArrowUpRightIcon} label="Расходы" value={s ? formatMoney(s.expense.sum) : <Skeleton className="h-7 w-20" />} />
      </div>
      <StatCard
        tone="hero"
        icon={WalletIcon}
        label="Остаток за период"
        value={s ? (s.balance < 0 ? "−" : "") + formatMoney(Math.abs(s.balance)) : <Skeleton className="h-7 w-28 bg-hero-foreground/15" />}
        pill={<Pill tone="hero">доходы − расходы</Pill>}
      />

      {empty ? (
        <Card className="px-5 py-6 text-center text-[15px] text-muted-foreground">{texts.analyticsEmpty}</Card>
      ) : (
        <>
          <Flow query={query} />
          <Panel icon={ChartPieIcon} title="Куда уходят деньги">
            <Distribution items={s?.expense.categories} total={s?.expense.sum} />
          </Panel>
          {(s?.income.count ?? 0) > 0 && (
            <Panel icon={ArrowDownLeftIcon} title="Откуда приходят">
              <Ranked items={s!.income.categories} total={s!.income.sum} />
            </Panel>
          )}
        </>
      )}
    </>
  );
}

/** Expenses or incomes over time: one series at a time, as a chart or a list. */
function Flow({ query }: { query: OperationQuery }) {
  const [kind, setKind] = useState<Kind>("expense");
  const [asList, setAsList] = useState(false);
  const bucket = bucketFor(query.from, query.to);
  const series = useTimeSeries({ ...query, kind }, bucket);
  const points = series.data?.items;

  return (
    <Panel
      icon={TrendingUpIcon}
      title={`Динамика ${BUCKET_TITLE[bucket]}`}
      action={
        <button
          type="button"
          aria-pressed={asList}
          aria-label={asList ? "Показать графиком" : "Показать списком"}
          onClick={() => setAsList((v) => !v)}
          className="flex size-10 items-center justify-center rounded-full border border-input"
        >
          {asList ? <ChartColumnIcon className="size-[18px]" /> : <ListIcon className="size-[18px]" />}
        </button>
      }
    >
      <Segmented
        label="Что показать"
        options={[
          { value: "expense", label: "Расходы" },
          { value: "income", label: "Доходы" },
        ]}
        value={kind}
        onChange={setKind}
      />
      {!points ? (
        <Skeleton className="h-52 w-full rounded-2xl" />
      ) : asList ? (
        <ul className="flex max-h-72 flex-col divide-y overflow-y-auto text-[15px]">
          {points
            .filter((p) => p.sum > 0)
            .reverse()
            .map((p) => (
              <li key={p.start} className="flex items-baseline justify-between gap-3 py-2.5">
                <span className="text-muted-foreground">{rangeLabel(p.start, bucket)}</span>
                <span className="font-medium tabular-nums">{formatMoney(p.sum)}</span>
              </li>
            ))}
        </ul>
      ) : (
        <FlowChart points={points} bucket={bucket} />
      )}
    </Panel>
  );
}

/** Categories as a ranked list with ink share bars. */
function Ranked({ items, total }: { items: CategoryTotal[]; total: number }) {
  const categories = useCategoryInfo();
  return (
    <ul className="flex flex-col gap-3">
      {items.map((t) => {
        const Icon = categoryIcons[t.category];
        const share = total ? t.sum / total : 0;
        return (
          <li key={t.category} className="flex items-center gap-3">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-full bg-muted">
              <Icon className="size-[18px]" strokeWidth={1.75} />
            </span>
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <div className="flex items-baseline justify-between gap-2 text-[15px]">
                <span className="truncate">{capitalize(categories.label(t.category))}</span>
                <span className="shrink-0 font-medium tabular-nums">{formatMoney(t.sum)}</span>
              </div>
              <div className="flex items-center gap-2">
                <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-muted">
                  <div className="h-full rounded-full bg-foreground" style={{ width: `${Math.max(share * 100, 1)}%` }} />
                </div>
                <span className="w-10 shrink-0 text-right text-[13px] text-muted-foreground tabular-nums">{formatShare(share)}</span>
              </div>
            </div>
          </li>
        );
      })}
    </ul>
  );
}
