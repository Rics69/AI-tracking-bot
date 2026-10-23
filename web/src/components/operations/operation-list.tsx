"use client";

import type { Operation } from "@/api/client";
import { Card, IconCircle } from "@/components/kit";
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
  /** Group by day, each day in its own card with a header. */
  grouped?: boolean;
  skeletonRows?: number;
};

export function OperationList({ items, myId, memberName, grouped = false, skeletonRows = 5 }: Props) {
  if (!items) {
    return (
      <div className="flex flex-col gap-3" aria-busy>
        {Array.from({ length: skeletonRows }, (_, i) => (
          <Skeleton key={i} className="h-12 w-full rounded-2xl" />
        ))}
      </div>
    );
  }
  if (!grouped) {
    return (
      <ul className="-mx-1 flex flex-col">
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
    <div className="flex flex-col gap-3">
      {days.map((d) => (
        <Card key={d.date} className="px-3 pt-3 pb-1.5">
          <h3 className="px-1 pb-1 text-[13px] text-muted-foreground">{formatDayHeader(d.date)}</h3>
          <ul className="flex flex-col">
            {d.ops.map((op) => (
              <OperationRow key={op.id} op={op} myId={myId} memberName={memberName} />
            ))}
          </ul>
        </Card>
      ))}
    </div>
  );
}

function OperationRow({ op, myId, memberName }: { op: Operation; myId?: number; memberName?: (id: number) => string }) {
  const sheet = useOperationSheet();
  const categories = useCategoryInfo();
  const own = op.user_id === myId;
  const label = capitalize(categories.label(op.category));
  const subtitle = [op.comment ? label : null, !own && memberName ? memberName(op.user_id) : null]
    .filter(Boolean)
    .join(" · ");

  const body = (
    <>
      <IconCircle icon={categoryIcons[op.category]} className="size-10" />
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate text-[15px] font-medium">{op.comment || label}</span>
        {subtitle && <span className="truncate text-[13px] text-muted-foreground">{subtitle}</span>}
      </span>
      <span
        className={cn(
          "shrink-0 text-[15px] font-medium tabular-nums",
          op.kind === "income" ? "text-success" : "text-foreground",
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
          className="flex min-h-14 w-full items-center gap-3 rounded-2xl px-1 py-1.5 text-left transition-colors active:bg-muted"
        >
          {body}
        </button>
      ) : (
        <div className="flex min-h-14 items-center gap-3 px-1 py-1.5">{body}</div>
      )}
    </li>
  );
}
