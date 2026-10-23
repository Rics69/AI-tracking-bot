"use client";

import {
  ArrowDownLeftIcon,
  ArrowUpRightIcon,
  CalendarDaysIcon,
  ChartPieIcon,
  ListIcon,
  WalletIcon,
} from "lucide-react";
import Image from "next/image";
import Link from "next/link";
import { useEffect, useState } from "react";

import type { SavikState } from "@/api/client";
import { useMe, useRecentOperations, useSavik, useSummary } from "@/api/queries";
import { GoalsPanel } from "@/components/budget/goals";
import { LimitCard } from "@/components/budget/limit-card";
import { Card, Panel, Pill, StatCard } from "@/components/kit";
import { CategoryColors, Distribution } from "@/components/money/distribution";
import { OperationList } from "@/components/operations/operation-list";
import { useOperationSheet } from "@/components/operations/sheet-context";
import { PhraseText, Savik } from "@/components/savik/savik";
import { Skeleton } from "@/components/ui/skeleton";
import type { Mood } from "@/content/savik";
import { texts } from "@/content/texts";
import { formatMoney, formatMonth, plural, toISODate } from "@/lib/format";

export function HomeScreen() {
  const me = useMe();
  const savik = useSavik();
  const recent = useRecentOperations();
  const mood = useMood(savik.data?.state);
  // The home screen is personal, even for a family head: own money only.
  const [range] = useState(() => {
    const now = new Date();
    return {
      from: toISODate(new Date(now.getFullYear(), now.getMonth(), 1)),
      today: toISODate(now),
    };
  });
  const own = me.data?.telegram_id;
  const month = useSummary({ userId: own, from: range.from, to: range.today });
  const today = useSummary({ userId: own, from: range.today, to: range.today, kind: "expense" });
  const m = month.data;
  const hasIncome = (m?.income.count ?? 0) > 0;

  return (
    <>
      <CategoryColors />
      <TopBar name={me.data?.first_name} />

      <div className="flex flex-col gap-1 px-1">
        <p className="text-[13px] text-muted-foreground">
          Сейвпоинт <span className="px-1">›</span> <span className="text-foreground">Обзор</span>
        </p>
        <h1 className="text-[32px] leading-tight font-medium tracking-tight">{formatMonth()}</h1>
      </div>

      <Card className="flex items-center gap-3 p-3 pr-4">
        <Savik mood={mood} size={92} phrase={null} />
        <SavikSays mood={mood} income={m?.income.sum} expense={m?.expense.sum} />
      </Card>

      <LimitCard />

      <div className="grid grid-cols-2 gap-3">
        <StatCard
          tone="hero"
          icon={WalletIcon}
          label={hasIncome ? "Остаток" : "Потрачено"}
          value={m ? money(hasIncome ? m.balance : m.expense.sum) : <Skeleton className="h-7 w-24 bg-hero-foreground/15" />}
          pill={<Pill tone="hero">{hasIncome ? "доходы − расходы" : "за месяц"}</Pill>}
        />
        <StatCard
          tone="soft"
          icon={ArrowDownLeftIcon}
          label="Доходы"
          value={m ? formatMoney(m.income.sum) : <Skeleton className="h-7 w-20" />}
          pill={<Pill tone="soft">{count(m?.income.count, ["поступление", "поступления", "поступлений"])}</Pill>}
        />
        <StatCard
          icon={ArrowUpRightIcon}
          label="Расходы"
          value={m ? formatMoney(m.expense.sum) : <Skeleton className="h-7 w-20" />}
          pill={<Pill>{count(m?.expense.count, ["трата", "траты", "трат"])}</Pill>}
        />
        <StatCard
          icon={CalendarDaysIcon}
          label="Сегодня"
          value={today.data ? formatMoney(today.data.expense.sum) : <Skeleton className="h-7 w-16" />}
          pill={
            <Pill tone={today.data?.expense.count ? "neutral" : "success"}>
              {today.data?.expense.count ? count(today.data.expense.count, ["трата", "траты", "трат"]) : "без трат"}
            </Pill>
          }
        />
      </div>

      <GoalsPanel />

      <Panel icon={ChartPieIcon} title="Куда уходят деньги" action={<Pill>{formatMonth()}</Pill>}>
        <Distribution items={m?.expense.categories} total={m?.expense.sum} />
      </Panel>

      <Panel
        icon={ListIcon}
        title="Последние"
        action={
          <Link href="/history" className="inline-flex h-9 items-center rounded-full border border-input px-4 text-[14px] font-medium">
            Все
          </Link>
        }
      >
        {recent.data?.length === 0 ? <EmptyRecent /> : <OperationList items={recent.data} myId={own} skeletonRows={3} />}
      </Panel>
    </>
  );
}

function money(v: number) {
  return (v < 0 ? "−" : "") + formatMoney(Math.abs(v));
}

function count(n: number | undefined, forms: [string, string, string]) {
  if (n === undefined) return "…";
  return `${n} ${plural(n, forms)}`;
}

/** The brand mark on the left, the user's initial on the right (to the profile). */
function TopBar({ name }: { name?: string }) {
  return (
    <div className="flex items-center justify-between">
      <div className="flex items-center gap-3">
        <span className="flex size-11 items-center justify-center overflow-hidden rounded-2xl bg-hero">
          <Image src="/savik/256/01-neutral.webp" alt="" width={40} height={40} unoptimized />
        </span>
        <div className="flex flex-col leading-tight">
          <span className="text-[17px] font-medium">{texts.appName}</span>
          <span className="text-[12px] text-muted-foreground">Копи XP</span>
        </div>
      </div>
      <Link
        href="/profile"
        aria-label="Профиль"
        className="flex size-11 items-center justify-center rounded-full bg-soft text-[15px] font-medium text-soft-foreground"
      >
        {(name ?? "·").charAt(0).toUpperCase()}
      </Link>
    </div>
  );
}

/** Savik's line and, when incomes are recorded, how much of them the month has eaten. */
function SavikSays({ mood, income, expense }: { mood: Mood; income?: number; expense?: number }) {
  const share = income && expense !== undefined ? Math.min(expense / income, 1) : undefined;
  return (
    <div className="flex min-w-0 flex-1 flex-col gap-2">
      <SavikLine mood={mood} />
      {share !== undefined && (
        <div className="flex flex-col gap-1">
          <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full rounded-full bg-foreground transition-[width] duration-500"
              style={{ width: `${Math.max(share * 100, 2)}%` }}
            />
          </div>
          <span className="text-[12px] text-muted-foreground">
            потрачено {Math.round(share * 100)}% доходов месяца
          </span>
        </div>
      )}
    </div>
  );
}

function SavikLine({ mood }: { mood: Mood }) {
  // The phrase lives in a bubble of its own component; here it is plain text.
  return (
    <p className="text-[15px] leading-snug" role="status">
      <PhraseText mood={mood} />
    </p>
  );
}

function EmptyRecent() {
  const sheet = useOperationSheet();
  return (
    <button type="button" onClick={() => sheet.openNew()} className="rounded-2xl bg-muted px-4 py-5 text-center text-[15px] text-muted-foreground">
      {texts.homeEmpty}
    </button>
  );
}

/** The month's mood, with a short burst of joy after each saved operation. */
function useMood(state: SavikState | undefined): Mood {
  const { saves } = useOperationSheet();
  // Saves up to this count have been celebrated already.
  const [celebrated, setCelebrated] = useState(saves);
  useEffect(() => {
    if (saves === celebrated) return;
    const t = setTimeout(() => setCelebrated(saves), 2600);
    return () => clearTimeout(t);
  }, [saves, celebrated]);
  if (saves !== celebrated) return "saved";
  return state ?? "neutral";
}

