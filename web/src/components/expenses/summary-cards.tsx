"use client";

import type { Summary } from "@/api/client";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { formatMoney, formatShare, plural } from "@/lib/format";

import { capitalize } from "./filters-bar";

type Props = {
  summary?: Summary;
  categoryLabel: (code: string) => string;
  periodLabel: string;
};

export function SummaryCards({ summary, categoryLabel, periodLabel }: Props) {
  // Each bar is the category's share of all spending in the selection.
  const share = (sum: number) => (summary && summary.sum > 0 ? sum / summary.sum : 0);

  return (
    <div className="grid gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,2fr)]">
      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium text-muted-foreground">
            Потрачено · {periodLabel.toLowerCase()}
          </CardTitle>
        </CardHeader>
        <CardContent className="flex flex-col gap-2">
          {summary ? (
            <>
              <p className="text-3xl font-semibold tracking-tight tabular-nums">
                {formatMoney(summary.sum)}
              </p>
              <p className="text-sm text-muted-foreground">
                {summary.count} {plural(summary.count, ["трата", "траты", "трат"])}
              </p>
            </>
          ) : (
            <>
              <Skeleton className="h-9 w-40" />
              <Skeleton className="h-5 w-20" />
            </>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle className="text-sm font-medium text-muted-foreground">
            По категориям · доля от всех трат
          </CardTitle>
        </CardHeader>
        <CardContent>
          {!summary ? (
            <div className="flex flex-col gap-3">
              {[0, 1, 2].map((i) => (
                <Skeleton key={i} className="h-5 w-full" />
              ))}
            </div>
          ) : summary.items.length === 0 ? (
            <p className="text-sm text-muted-foreground">Нет трат за выбранный период</p>
          ) : (
            <ul className="flex flex-col gap-3">
              {summary.items.map((item) => (
                <li
                  key={item.category}
                  className="grid grid-cols-[6.5rem_minmax(0,1fr)_3rem_auto] items-center gap-3 text-sm"
                >
                  <span className="truncate">{capitalize(categoryLabel(item.category))}</span>
                  <span className="h-2 overflow-hidden rounded-full bg-muted" aria-hidden>
                    <span
                      className="block h-full rounded-full bg-primary"
                      style={{ width: `${share(item.sum) * 100}%` }}
                    />
                  </span>
                  <span className="text-right text-muted-foreground tabular-nums">
                    {formatShare(share(item.sum))}
                  </span>
                  <span className="text-right tabular-nums">{formatMoney(item.sum)}</span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
