"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import type { Kind } from "@/api/client";
import { useFamily, useMe, useOperations, type OperationQuery } from "@/api/queries";
import { OperationList } from "@/components/operations/operation-list";
import { Chips } from "@/components/shell/chips";
import { Button } from "@/components/ui/button";
import { texts } from "@/content/texts";
import { formatMoney } from "@/lib/format";
import { periodRange, PERIODS, type Period } from "@/lib/period";

type KindFilter = "all" | Kind;
type PeriodFilter = Exclude<Period, "custom">;

const kinds: { value: KindFilter; label: string }[] = [
  { value: "all", label: "Всё" },
  { value: "expense", label: "Расходы" },
  { value: "income", label: "Доходы" },
];

const periods = (
  ["this-month", "last-month", "this-week", "last-30", "this-year", "all"] as const
).map((p) => ({
  value: p,
  label: PERIODS[p],
}));

export function HistoryScreen() {
  const me = useMe();
  const family = useFamily();
  const [kind, setKind] = useState<KindFilter>("all");
  const [period, setPeriod] = useState<PeriodFilter>("this-month");
  const [member, setMember] = useState("all");

  const query = useMemo<OperationQuery>(() => {
    const { from, to } = periodRange(period);
    return {
      kind: kind === "all" ? undefined : kind,
      userId: member === "all" ? undefined : Number(member),
      from,
      to,
    };
  }, [kind, period, member]);
  const ops = useOperations(query);
  const pages = ops.data?.pages;
  const items = useMemo(() => pages?.flatMap((p) => p.items), [pages]);
  const totals = pages?.[0];

  const members = useMemo(() => family.data?.members ?? [], [family.data]);
  const names = useMemo(
    () => new Map(members.map((m) => [m.telegram_id, m.first_name])),
    [members],
  );
  const showMembers = me.data?.is_head && members.length > 1;

  return (
    <>
      <h1 className="px-1 text-2xl font-semibold tracking-tight">История</h1>

      <div className="flex flex-col gap-2">
        <Chips label="Что показать" options={kinds} value={kind} onChange={setKind} />
        <Chips label="Период" options={periods} value={period} onChange={setPeriod} />
        {showMembers && (
          <Chips
            label="Член семьи"
            options={[
              { value: "all", label: "Вся семья" },
              ...members.map((m) => ({
                value: String(m.telegram_id),
                label: m.telegram_id === me.data?.telegram_id ? "Я" : m.first_name,
              })),
            ]}
            value={member}
            onChange={setMember}
          />
        )}
      </div>

      {totals && (
        <p className="px-1 text-[15px] text-muted-foreground">
          {kind !== "income" && (
            <>
              Потрачено{" "}
              <span className="font-semibold text-foreground tabular-nums">
                {formatMoney(totals.expense_sum)}
              </span>
            </>
          )}
          {kind === "all" && " · "}
          {kind !== "expense" && (
            <>
              Получено{" "}
              <span className="font-semibold text-primary tabular-nums">
                {formatMoney(totals.income_sum)}
              </span>
            </>
          )}
        </p>
      )}

      {ops.error ? (
        <div className="flex flex-col items-center gap-3 rounded-2xl bg-card p-6 text-center">
          <p className="text-[15px] text-muted-foreground">{ops.error.message}</p>
          <Button variant="secondary" className="h-11" onClick={() => void ops.refetch()}>
            Повторить
          </Button>
        </div>
      ) : items?.length === 0 ? (
        <p className="rounded-2xl bg-card px-5 py-6 text-center text-[15px] text-muted-foreground">
          {texts.historyEmpty}
        </p>
      ) : (
        <OperationList
          items={items}
          myId={me.data?.telegram_id}
          memberName={(id) => names.get(id) ?? "участник"}
          grouped
        />
      )}

      <LoadMore
        active={!!ops.hasNextPage && !ops.isFetchingNextPage}
        onVisible={() => void ops.fetchNextPage()}
      />
    </>
  );
}

/** Loads the next page when the end of the list scrolls into view. */
function LoadMore({ active, onVisible }: { active: boolean; onVisible: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || !active) return;
    const observer = new IntersectionObserver(
      (entries) => entries[0].isIntersecting && onVisible(),
      {
        rootMargin: "400px",
      },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [active, onVisible]);
  return <div ref={ref} aria-hidden className="h-1" />;
}
