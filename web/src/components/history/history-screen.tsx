"use client";

import { useEffect, useMemo, useRef, useState } from "react";

import type { Kind } from "@/api/client";
import { useFamily, useMe, useOperations, type OperationQuery } from "@/api/queries";
import { Card, PageHeader, PillButton, Segmented } from "@/components/kit";
import { OperationList } from "@/components/operations/operation-list";
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

const periods = (["this-month", "last-month", "this-week", "last-30", "this-year", "all"] as const).map((p) => ({
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
  const names = useMemo(() => new Map(members.map((m) => [m.telegram_id, m.first_name])), [members]);
  const showMembers = me.data?.is_head && members.length > 1;

  return (
    <>
      <PageHeader title="История" />

      <div className="flex flex-col gap-2">
        <Segmented label="Что показать" options={kinds} value={kind} onChange={setKind} />
        <Segmented label="Период" options={periods} value={period} onChange={setPeriod} />
        {showMembers && (
          <Segmented
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
        <div className="grid grid-cols-2 gap-3">
          {kind !== "income" && <Total label="Потрачено" value={totals.expense_sum} />}
          {kind !== "expense" && <Total label="Получено" value={totals.income_sum} soft />}
        </div>
      )}

      {ops.error ? (
        <Card className="flex flex-col items-center gap-3 p-6 text-center">
          <p className="text-[15px] text-muted-foreground">{ops.error.message}</p>
          <PillButton variant="outline" onClick={() => void ops.refetch()}>
            Повторить
          </PillButton>
        </Card>
      ) : items?.length === 0 ? (
        <Card className="px-5 py-6 text-center text-[15px] text-muted-foreground">{texts.historyEmpty}</Card>
      ) : (
        <OperationList
          items={items}
          myId={me.data?.telegram_id}
          memberName={(id) => names.get(id) ?? "участник"}
          grouped
        />
      )}

      <LoadMore active={!!ops.hasNextPage && !ops.isFetchingNextPage} onVisible={() => void ops.fetchNextPage()} />
    </>
  );
}

function Total({ label, value, soft }: { label: string; value: number; soft?: boolean }) {
  return (
    <div className={soft ? "rounded-[22px] bg-soft p-4 text-soft-foreground" : "rounded-[22px] border border-border bg-card p-4"}>
      <p className={soft ? "text-[13px]" : "text-[13px] text-muted-foreground"}>{label}</p>
      <p className="pt-1 text-[22px] font-medium tracking-tight tabular-nums">{formatMoney(value)}</p>
    </div>
  );
}

/** Loads the next page when the end of the list scrolls into view. */
function LoadMore({ active, onVisible }: { active: boolean; onVisible: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el || !active) return;
    const observer = new IntersectionObserver((entries) => entries[0].isIntersecting && onVisible(), {
      rootMargin: "400px",
    });
    observer.observe(el);
    return () => observer.disconnect();
  }, [active, onVisible]);
  return <div ref={ref} aria-hidden className="h-1" />;
}
