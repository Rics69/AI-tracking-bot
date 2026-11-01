"use client";

import { useRates } from "@/api/queries";
import { Card } from "@/components/kit";
import { currencySign, formatDate, formatMoney } from "@/lib/format";

/**
 * Dollar and euro by the Bank of Russia, with the day of the rate. Without
 * rates there is nothing to show, and the screen stays as it is.
 */
export function RatesWidget() {
  const items = useRates().data?.items;
  if (!items || items.length === 0) return null;
  const date = items.map((r) => r.date).sort().at(-1)!;
  return (
    <Card className="flex items-center justify-between gap-3 px-4 py-3">
      <span className="flex flex-wrap gap-x-4 text-[15px] font-medium tabular-nums">
        {[...items].sort((a, b) => b.code.localeCompare(a.code)).map((r) => (
          <span key={r.code}>
            {currencySign(r.code)} <span className="text-gold">{formatMoney(r.value)}</span>
          </span>
        ))}
      </span>
      <span className="shrink-0 text-[12px] text-muted-foreground">ЦБ, {formatDate(date)}</span>
    </Card>
  );
}
