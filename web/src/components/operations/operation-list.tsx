"use client";

import type { Operation } from "@/api/client";
import { Skeleton } from "@/components/ui/skeleton";
import { capitalize, categoryIcons, useCategoryInfo } from "@/lib/categories";
import { formatDayHeader, formatSigned } from "@/lib/format";
import { cn } from "@/lib/utils";

import { useOperationSheet } from "./sheet-context";

type Props = {
  items: Operation[] | undefined;
  /** The viewer: only their own operations open for editing. */
  myId: number | undefined;
  /** Names of family members, shown on operations that are not the viewer's. */
  memberName?: (id: number) => string;
  /** Group by day with headers ("Сегодня", "16 октября"). */
  grouped?: boolean;
  skeletonRows?: number;
};

export function OperationList({
  items,
  myId,
  memberName,
  grouped = false,
  skeletonRows = 5,
}: Props) {
  if (!items) {
    return (
      <div className="flex flex-col gap-2 rounded-2xl bg-card p-2" aria-busy>
        {Array.from({ length: skeletonRows }, (_, i) => (
          <Skeleton key={i} className="h-14 w-full rounded-xl" />
        ))}
      </div>
    );
  }
  if (!grouped) {
    return (
      <ul className="flex flex-col rounded-2xl bg-card py-1">
        {items.map((op) => (
          <OperationRow key={op.id} op={op} myId={myId} memberName={memberName} />
        ))}
      </ul>
    );
  }
  const days: { date: string; ops: Operation[] }[] = [];
  for (const op of items) {
    const last = days.at(-1);
    if (last?.date === op.date) last.ops.push(op);
    else days.push({ date: op.date, ops: [op] });
  }
  return (
    <div className="flex flex-col gap-4">
      {days.map((d) => (
        <section key={d.date} className="flex flex-col gap-2">
          <h3 className="px-1 text-sm font-medium text-muted-foreground">
            {formatDayHeader(d.date)}
          </h3>
          <ul className="flex flex-col rounded-2xl bg-card py-1">
            {d.ops.map((op) => (
              <OperationRow key={op.id} op={op} myId={myId} memberName={memberName} />
            ))}
          </ul>
        </section>
      ))}
    </div>
  );
}

function OperationRow({
  op,
  myId,
  memberName,
}: {
  op: Operation;
  myId?: number;
  memberName?: (id: number) => string;
}) {
  const sheet = useOperationSheet();
  const categories = useCategoryInfo();
  const Icon = categoryIcons[op.category];
  const own = op.user_id === myId;
  const label = capitalize(categories.label(op.category));
  const subtitle = [op.comment ? label : null, !own && memberName ? memberName(op.user_id) : null]
    .filter(Boolean)
    .join(" · ");

  const body = (
    <>
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-muted text-muted-foreground">
        <Icon className="size-5" />
      </span>
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-[15px] font-medium">{op.comment || label}</span>
        {subtitle && <span className="truncate text-[13px] text-muted-foreground">{subtitle}</span>}
      </span>
      <span
        className={cn(
          "shrink-0 text-[15px] font-semibold tabular-nums",
          op.kind === "income" ? "text-primary" : "text-foreground",
        )}
      >
        {formatSigned(op.amount, op.kind)}
      </span>
    </>
  );

  return (
    <li>
      {own ? (
        <button
          type="button"
          onClick={() => sheet.openEdit(op)}
          className="flex min-h-14 w-full items-center gap-3 px-3 py-2 text-left transition-colors active:bg-muted/60"
        >
          {body}
        </button>
      ) : (
        <div className="flex min-h-14 items-center gap-3 px-3 py-2">{body}</div>
      )}
    </li>
  );
}
