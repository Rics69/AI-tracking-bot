"use client";

import { GaugeIcon, Settings2Icon } from "lucide-react";
import Link from "next/link";

import type { BudgetToday } from "@/api/client";
import { useBudget } from "@/api/queries";
import { Card, IconCircle, Panel, Pill, PillButton } from "@/components/kit";
import { useOperationSheet } from "@/components/operations/sheet-context";
import { Skeleton } from "@/components/ui/skeleton";
import { texts } from "@/content/texts";
import { formatDate, formatMoney, plural } from "@/lib/format";
import { cn } from "@/lib/utils";

/** Today's limit on the home screen, or an invitation to set it up. */
export function LimitCard() {
  const budget = useBudget();
  if (!budget.data) return <Skeleton className="h-56 w-full rounded-[22px]" />;
  const today = budget.data.today;
  if (!today) return <SetupCard />;

  return (
    <Panel
      icon={GaugeIcon}
      title="Лимит на сегодня"
      action={
        <Link
          href="/plan"
          aria-label="Настроить лимит"
          className="flex size-9 items-center justify-center rounded-full border border-input"
        >
          <Settings2Icon className="size-[18px]" strokeWidth={1.75} />
        </Link>
      }
    >
      {today.ready ? <Ready b={today} /> : <NoIncome />}
    </Panel>
  );
}

function Ready({ b }: { b: BudgetToday }) {
  // Nothing left until payday: there is no limit to go over, just say so.
  const broke = b.limit <= 0;
  const over = !broke && b.left < 0;
  const share = broke ? 1 : Math.min(b.spent_today / b.limit, 1);
  const next = b.upcoming[0];
  return (
    <div className="flex flex-col gap-4">
      <Gauge
        share={share}
        over={over || broke}
        label={`Потрачено ${formatMoney(b.spent_today)} из ${formatMoney(b.limit)}`}
      >
        <span className="text-[30px] leading-none font-medium tracking-tight tabular-nums">
          {over ? "−" + formatMoney(-b.left) : formatMoney(Math.max(b.left, 0))}
        </span>
        <span className={cn("text-[13px]", over || broke ? "text-destructive" : "text-muted-foreground")}>
          {broke ? "до зарплаты денег не осталось" : over ? "сверх лимита" : "можно ещё сегодня"}
        </span>
      </Gauge>

      <div className="grid grid-cols-2 gap-2">
        <Figure label="Лимит" value={formatMoney(b.limit)} />
        <Figure label="Потрачено" value={formatMoney(b.spent_today)} />
      </div>

      <div className="flex flex-wrap gap-2">
        <Pill>
          до зарплаты {b.days_left} {plural(b.days_left, ["день", "дня", "дней"])}
        </Pill>
        <Pill tone={b.remaining > 0 ? "neutral" : "danger"}>
          в запасе {b.remaining < 0 ? "−" : ""}
          {formatMoney(Math.abs(b.remaining))}
        </Pill>
        {next && (
          <Pill tone="warning">
            {next.title} · {formatDate(next.date)}
          </Pill>
        )}
      </div>
    </div>
  );
}

/**
 * A half-ring meter: today's spending against the limit. One series, so the
 * arc is ink on a muted track; over the limit it turns red and says so in words.
 */
function Gauge({
  share,
  over,
  label,
  children,
}: {
  share: number;
  over: boolean;
  label: string;
  children: React.ReactNode;
}) {
  const arc = "M 16 100 A 84 84 0 0 1 184 100";
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={Math.round(share * 100)}
      className="relative mx-auto w-full max-w-[280px]"
    >
      <svg viewBox="0 0 200 108" className="w-full" aria-hidden>
        <path d={arc} pathLength={100} fill="none" strokeWidth={14} strokeLinecap="round" className="stroke-muted" />
        <path
          d={arc}
          pathLength={100}
          fill="none"
          strokeWidth={14}
          strokeLinecap="round"
          strokeDasharray={`${Math.max(share * 100, 0.5)} 100`}
          className={cn(
            "transition-[stroke-dasharray] duration-700 ease-out",
            over ? "stroke-destructive" : "stroke-foreground",
          )}
        />
      </svg>
      <div className="absolute inset-x-0 bottom-0 flex flex-col items-center gap-1">{children}</div>
    </div>
  );
}

function Figure({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-2xl bg-muted px-3 py-2.5">
      <span className="text-[12px] text-muted-foreground">{label}</span>
      <span className="text-[17px] font-medium tabular-nums">{value}</span>
    </div>
  );
}

function NoIncome() {
  const sheet = useOperationSheet();
  return (
    <div className="flex flex-col gap-3">
      <p className="text-[15px] leading-snug text-muted-foreground">{texts.limitNoIncome}</p>
      <PillButton variant="soft" className="self-start" onClick={() => sheet.openNew("income")}>
        Записать доход
      </PillButton>
    </div>
  );
}

function SetupCard() {
  return (
    <Card className="flex items-center gap-3 bg-soft p-4 text-soft-foreground">
      <IconCircle icon={GaugeIcon} tone="soft" />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-[16px] font-medium">{texts.limitSetupTitle}</span>
        <span className="text-[13px] leading-snug opacity-80">{texts.limitSetupBody}</span>
      </div>
      <Link
        href="/plan"
        className="inline-flex h-9 shrink-0 items-center rounded-full bg-soft-foreground px-4 text-[14px] font-medium text-soft"
      >
        Настроить
      </Link>
    </Card>
  );
}
