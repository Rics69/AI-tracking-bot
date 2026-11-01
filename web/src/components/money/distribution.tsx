"use client";

import type { Category, CategoryTotal } from "@/api/client";
import { Skeleton } from "@/components/ui/skeleton";
import { capitalize, useCategoryInfo } from "@/lib/categories";
import { formatMoney, formatShare } from "@/lib/format";

// Expense categories keep their colour on every screen and under every filter:
// colour follows the category, never its rank. The validated categorical
// palette in its fixed order, checked on the card surface (#161616): adjacent
// CVD ΔE ≥ 8.4, normal-vision ≥ 19.3.
const CATEGORY_COLORS: Partial<Record<Category, string>> = {
  food: "#3987e5",
  transport: "#d95926",
  housing: "#199e70",
  entertainment: "#c98500",
  health: "#d55181",
  shopping: "#008300",
  other: "#9085e9",
};

/** Defines --cat-<code>: rendered once per screen that shows categories. */
export function CategoryColors() {
  const colors = Object.entries(CATEGORY_COLORS).map(([c, v]) => `--cat-${c}: ${v};`).join(" ");
  return <style>{`:root { ${colors} }`}</style>;
}

/**
 * Where the money goes: one stacked bar of expense categories (2px gaps
 * between segments) and a legend with amounts and shares.
 */
export function Distribution({ items, total }: { items?: CategoryTotal[]; total?: number }) {
  const categories = useCategoryInfo();
  if (!items || total === undefined) return <Skeleton className="h-36 w-full rounded-2xl" />;
  if (items.length === 0 || total <= 0) {
    return <p className="text-[15px] text-muted-foreground">Трат за этот период нет.</p>;
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="flex h-3 w-full gap-0.5 overflow-hidden rounded-full" role="img" aria-label="Доли категорий">
        {items.map((t) => (
          <span
            key={t.category}
            className="h-full first:rounded-l-full last:rounded-r-full"
            style={{ width: `${(t.sum / total) * 100}%`, minWidth: 3, background: `var(--cat-${t.category})` }}
          />
        ))}
      </div>
      <ul className="grid grid-cols-1 gap-x-6 gap-y-2.5">
        {items.map((t) => (
          <li key={t.category} className="grid grid-cols-[auto_1fr_auto_3rem] items-center gap-2.5 text-[15px]">
            <span className="size-2.5 rounded-[3px]" style={{ background: `var(--cat-${t.category})` }} aria-hidden />
            <span className="truncate">{capitalize(categories.label(t.category))}</span>
            <span className="font-medium tabular-nums">{formatMoney(t.sum)}</span>
            <span className="text-right text-[13px] text-muted-foreground tabular-nums">{formatShare(t.sum / total)}</span>
          </li>
        ))}
      </ul>
    </div>
  );
}
