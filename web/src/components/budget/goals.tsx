"use client";

import { PlusIcon, TargetIcon, Trash2Icon } from "lucide-react";
import Link from "next/link";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { ApiError, type Goal } from "@/api/client";
import { useDeleteGoal, useDeposit, useGoals } from "@/api/queries";
import { Panel, Pill, PillButton } from "@/components/kit";
import { confirm } from "@/components/operations/operation-sheet";
import { Sheet } from "@/components/ui/sheet";
import { amountInput, parseAmount } from "@/lib/amount";
import { formatDate, formatMoney } from "@/lib/format";
import { haptic } from "@/lib/telegram";
import { cn } from "@/lib/utils";

/** Goals on the home screen; nothing when there are none. */
export function GoalsPanel() {
  const goals = useGoals();
  const [depositTo, setDepositTo] = useState<Goal | null>(null);
  if (!goals.data?.length) return null;
  return (
    <Panel
      icon={TargetIcon}
      title="Цели"
      action={
        <Link href="/plan" className="inline-flex h-9 items-center rounded-full border border-input px-4 text-[14px] font-medium">
          Все
        </Link>
      }
    >
      <ul className="flex flex-col gap-4">
        {goals.data.slice(0, 3).map((g) => (
          <li key={g.id}>
            <GoalRow goal={g} onDeposit={() => setDepositTo(g)} />
          </li>
        ))}
      </ul>
      <DepositSheet goal={depositTo} onClose={() => setDepositTo(null)} />
    </Panel>
  );
}

/** A goal with its progress and a "+" to put money aside; optionally deletable. */
export function GoalRow({ goal: g, onDeposit, deletable }: { goal: Goal; onDeposit: () => void; deletable?: boolean }) {
  const remove = useDeleteGoal();
  const reached = g.left <= 0;
  const share = Math.min(g.saved / g.target, 1);

  async function onDelete() {
    if (!(await confirm(`Удалить цель «${g.title}»? Отложенные на неё деньги снова станут свободными.`))) return;
    try {
      await remove.mutateAsync(g.id);
      haptic.success();
      toast.success("Цель удалена");
    } catch (err) {
      haptic.error();
      toast.error(err instanceof Error ? err.message : "Не удалось удалить");
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 flex-col">
          <span className="truncate text-[16px] font-medium">{g.title}</span>
          <span className="text-[13px] text-muted-foreground tabular-nums">
            {formatMoney(g.saved)} из {formatMoney(g.target)}
            {g.deadline && ` · к ${formatDate(g.deadline)}`}
          </span>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {reached ? <Pill tone="success">накоплено</Pill> : <Pill>ещё {formatMoney(g.left)}</Pill>}
          {deletable && (
            <button
              type="button"
              aria-label={`Удалить цель «${g.title}»`}
              onClick={() => void onDelete()}
              className="flex size-9 items-center justify-center rounded-full text-muted-foreground"
            >
              <Trash2Icon className="size-[18px]" strokeWidth={1.75} />
            </button>
          )}
          <button
            type="button"
            aria-label={`Отложить на «${g.title}»`}
            onClick={onDeposit}
            className="flex size-9 items-center justify-center rounded-full bg-primary text-primary-foreground"
          >
            <PlusIcon className="size-[18px]" strokeWidth={2} />
          </button>
        </div>
      </div>
      <div
        role="progressbar"
        aria-label={`Накоплено на «${g.title}»`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={Math.round(share * 100)}
        className="h-2 w-full overflow-hidden rounded-full bg-muted"
      >
        <div
          className={cn("h-full rounded-full transition-[width] duration-500", reached ? "bg-success" : "bg-foreground")}
          style={{ width: `${Math.max(share * 100, 2)}%` }}
        />
      </div>
    </div>
  );
}

/** Puts money aside for a goal or takes it back. */
export function DepositSheet({ goal, onClose }: { goal: Goal | null; onClose: () => void }) {
  return (
    <Sheet open={goal !== null} onOpenChange={(open) => !open && onClose()} title={goal ? goal.title : "Цель"}>
      {goal && <DepositForm key={goal.id} goal={goal} onClose={onClose} />}
    </Sheet>
  );
}

function DepositForm({ goal, onClose }: { goal: Goal; onClose: () => void }) {
  const [direction, setDirection] = useState<"in" | "out">("in");
  const [value, setValue] = useState("");
  const [error, setError] = useState<string>();
  const deposit = useDeposit();

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const parsed = parseAmount(value);
    if (typeof parsed !== "number") {
      setError(parsed.error);
      haptic.error();
      return;
    }
    if (direction === "out" && parsed > goal.saved) {
      setError(`Отложено только ${formatMoney(goal.saved)}`);
      haptic.error();
      return;
    }
    try {
      const g = await deposit.mutateAsync({ id: goal.id, amount: direction === "in" ? parsed : -parsed });
      haptic.success();
      toast.success(
        g.left <= 0 && goal.left > 0
          ? `Цель «${g.title}» накоплена!`
          : `${direction === "in" ? "Отложено" : "Забрано"}: ${formatMoney(parsed)}`,
      );
      onClose();
    } catch (err) {
      haptic.error();
      setError(err instanceof ApiError ? err.message : "Не удалось сохранить");
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-5 pb-2">
      <div role="radiogroup" aria-label="Что сделать" className="grid grid-cols-2 gap-1 rounded-full bg-accent p-1">
        {(["in", "out"] as const).map((d) => (
          <button
            key={d}
            type="button"
            role="radio"
            aria-checked={direction === d}
            onClick={() => {
              haptic.select();
              setDirection(d);
              setError(undefined);
            }}
            className={cn(
              "h-10 rounded-full text-[15px] font-medium transition-colors",
              direction === d ? "bg-card text-foreground shadow-sm" : "text-muted-foreground",
            )}
          >
            {d === "in" ? "Отложить" : "Забрать"}
          </button>
        ))}
      </div>
      <AmountField id="deposit-amount" value={value} onChange={(v) => { setValue(v); setError(undefined); }} error={error} autoFocus />
      <p className="text-center text-[13px] text-muted-foreground">
        Отложено {formatMoney(goal.saved)} из {formatMoney(goal.target)}. Отложенные деньги — не трата: они просто не попадают в лимит.
      </p>
      <PillButton type="submit" className="h-12 w-full" disabled={deposit.isPending}>
        {deposit.isPending ? "Сохранение…" : direction === "in" ? "Отложить" : "Забрать"}
      </PillButton>
    </form>
  );
}

/** A big centred amount input with "₽", as in the operation sheet. */
export function AmountField({
  id,
  value,
  onChange,
  error,
  autoFocus,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
  autoFocus?: boolean;
}) {
  return (
    <div className="flex flex-col items-center gap-1">
      <label htmlFor={id} className="sr-only">
        Сумма в рублях
      </label>
      <div className="flex items-baseline justify-center gap-2">
        <input
          id={id}
          inputMode="decimal"
          autoComplete="off"
          autoFocus={autoFocus}
          placeholder="0"
          value={value}
          onChange={(e) => onChange(amountInput(e.target.value))}
          aria-invalid={!!error}
          className="w-full max-w-60 bg-transparent text-center text-5xl font-semibold tracking-tight tabular-nums outline-none placeholder:text-muted-foreground/40"
        />
        <span className="text-3xl font-medium text-muted-foreground">₽</span>
      </div>
      {error && (
        <p role="alert" className="text-[13px] text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}
