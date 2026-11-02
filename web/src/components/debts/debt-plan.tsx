"use client";

import { CalculatorIcon } from "lucide-react";
import { useDeferredValue, useState } from "react";

import { ApiError, type DebtSchedule } from "@/api/client";
import { useDebtPlan } from "@/api/queries";
import { inputClass } from "@/components/accounts/accounts-screen";
import { Panel } from "@/components/kit";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { amountInput } from "@/lib/amount";
import { formatMoney, plural } from "@/lib/format";

import { monthLabel } from "./net-worth-chart";

/** Both methods side by side, with nothing marked as the better one: the choice is the user's. */
export function DebtPlan() {
  const [text, setText] = useState("");
  const typed = Number(text.replace(/\s/g, "").replace(",", "."));
  const extra = useDeferredValue(Number.isFinite(typed) ? typed : 0);
  const plan = useDebtPlan(extra, true);
  const never = plan.error instanceof ApiError ? plan.error.message : undefined;
  return (
    <Panel icon={CalculatorIcon} title="План погашения">
      <div className="flex flex-col gap-4">
        <div className="flex flex-col gap-2">
          <label htmlFor="plan-extra" className="text-sm font-medium text-muted-foreground">
            Сверх минимальных платежей, ₽ в месяц
          </label>
          <Input
            id="plan-extra"
            inputMode="decimal"
            placeholder="0"
            value={text}
            onChange={(e) => setText(amountInput(e.target.value))}
            className={inputClass}
          />
        </div>
        {never ? (
          <p role="alert" className="text-[14px] text-muted-foreground">
            {never}
          </p>
        ) : !plan.data ? (
          <Skeleton className="h-36 w-full rounded-2xl" />
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3">
              <Method title="Лавина" hint="Сначала самая высокая ставка" s={plan.data.avalanche} />
              <Method title="Снежный ком" hint="Сначала самый маленький остаток" s={plan.data.snowball} />
            </div>
            <p className="text-[13px] text-muted-foreground">
              Сумма платежей каждый месяц одна и та же, ставки считаются неизменными. Банк может считать проценты иначе.
            </p>
          </>
        )}
        <p className="text-[13px] font-medium text-muted-foreground">Это учёт, а не рекомендация. Решение за вами.</p>
      </div>
    </Panel>
  );
}

function Method({ title, hint, s }: { title: string; hint: string; s: DebtSchedule }) {
  return (
    <div className="flex min-w-0 flex-col gap-2 rounded-2xl bg-muted p-3">
      <span className="flex flex-col">
        <span className="text-[15px] font-medium">{title}</span>
        <span className="text-[12px] text-muted-foreground">{hint}</span>
      </span>
      <span className="flex flex-col text-[14px] tabular-nums">
        <span>
          {s.months} {plural(s.months, ["месяц", "месяца", "месяцев"])}
        </span>
        <span className="text-muted-foreground">Переплата {formatMoney(s.interest)}</span>
      </span>
      <ul className="flex flex-col gap-1 text-[12px] text-muted-foreground">
        {s.closings.map((c) => (
          <li key={c.name} className="flex flex-col">
            <span className="truncate text-foreground">{c.name}</span>
            <span>{monthLabel(c.month)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
