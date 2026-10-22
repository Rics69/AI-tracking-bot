"use client";

import { ChevronRightIcon } from "lucide-react";
import Link from "next/link";
import { useEffect, useState } from "react";

import { useMe, useRecentOperations, useSavik } from "@/api/queries";
import { OperationList } from "@/components/operations/operation-list";
import { useOperationSheet } from "@/components/operations/sheet-context";
import { Savik } from "@/components/savik/savik";
import { Skeleton } from "@/components/ui/skeleton";
import type { Mood } from "@/content/savik";
import { texts } from "@/content/texts";
import { formatMoney, formatMonth } from "@/lib/format";
import { cn } from "@/lib/utils";

export function HomeScreen() {
  const me = useMe();
  const savik = useSavik();
  const recent = useRecentOperations();
  const mood = useMood(savik.data?.state);
  const month = savik.data?.month;

  return (
    <>
      <Savik mood={mood} size={176} className="pt-2" />

      <section aria-label="Деньги месяца" className="flex flex-col gap-4 rounded-2xl bg-card p-5">
        <div className="flex items-center justify-between">
          <h2 className="text-[15px] font-medium text-muted-foreground">{formatMonth()}</h2>
          {/* Incomes are optional: without them a "balance" would only scare. */}
          <span className="text-[13px] text-muted-foreground">
            {month && month.income > 0 ? "остаток" : "потрачено"}
          </span>
        </div>
        {month ? (
          <p
            className={cn(
              "text-4xl font-semibold tracking-tight tabular-nums",
              month.income > 0 && month.balance < 0 && "text-destructive",
            )}
          >
            {month.income > 0
              ? (month.balance < 0 ? "−" : "") + formatMoney(Math.abs(month.balance))
              : formatMoney(month.expense)}
          </p>
        ) : (
          <Skeleton className="h-10 w-40" />
        )}
        <div className="grid grid-cols-2 gap-3">
          <Figure label="Доходы" value={month?.income} accent />
          <Figure label="Расходы" value={month?.expense} />
        </div>
      </section>

      <section className="flex flex-col gap-3">
        <div className="flex items-center justify-between px-1">
          <h2 className="text-lg font-semibold">Последние</h2>
          <Link
            href="/history"
            className="flex min-h-11 items-center gap-0.5 text-[15px] font-medium text-primary"
          >
            Все
            <ChevronRightIcon className="size-4" />
          </Link>
        </div>
        {recent.data?.length === 0 ? (
          <EmptyRecent />
        ) : (
          <OperationList items={recent.data} myId={me.data?.telegram_id} skeletonRows={3} />
        )}
      </section>
    </>
  );
}

function Figure({ label, value, accent }: { label: string; value?: number; accent?: boolean }) {
  return (
    <div className="flex flex-col gap-1 rounded-xl bg-muted px-3 py-2.5">
      <span className="text-[13px] text-muted-foreground">{label}</span>
      {value === undefined ? (
        <Skeleton className="h-6 w-20" />
      ) : (
        <span className={cn("text-lg font-semibold tabular-nums", accent && "text-primary")}>
          {formatMoney(value)}
        </span>
      )}
    </div>
  );
}

function EmptyRecent() {
  const sheet = useOperationSheet();
  return (
    <button
      type="button"
      onClick={() => sheet.openNew()}
      className="rounded-2xl bg-card px-5 py-6 text-center text-[15px] text-muted-foreground"
    >
      {texts.homeEmpty}
    </button>
  );
}

/** The month's mood, with a short burst of joy after each saved operation. */
function useMood(state: "neutral" | "empty" | undefined): Mood {
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
