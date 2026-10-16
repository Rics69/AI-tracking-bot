"use client";

import { useSearchParams } from "next/navigation";
import { useCallback, useMemo } from "react";

import type { Category } from "@/api/client";
import type { ExpenseQuery } from "@/api/queries";
import { DEFAULT_PERIOD, isPeriod, periodRange, type Period } from "@/lib/period";

export type Filters = {
  userId?: number;
  category?: Category;
  period: Period;
  from?: string; // only for the custom period
  to?: string;
  page: number; // 1-based
};

const CATEGORIES: readonly Category[] = [
  "food", "transport", "housing", "entertainment", "health", "shopping", "other",
];

/**
 * Filters live in the URL (?member=…&period=…), so a filtered view survives
 * a reload and can be shared as a link.
 */
export function useFilters() {
  const params = useSearchParams();

  const filters = useMemo<Filters>(() => {
    const member = Number(params.get("member"));
    const category = params.get("category") as Category | null;
    const period = params.get("period");
    const page = Number(params.get("page"));
    return {
      userId: Number.isSafeInteger(member) && member > 0 ? member : undefined,
      category: category && CATEGORIES.includes(category) ? category : undefined,
      period: isPeriod(period) ? period : DEFAULT_PERIOD,
      from: params.get("from") ?? undefined,
      to: params.get("to") ?? undefined,
      page: Number.isSafeInteger(page) && page > 1 ? page : 1,
    };
  }, [params]);

  const setFilters = useCallback(
    (patch: Partial<Filters>) => {
      // Any filter change except paging starts from the first page.
      const next: Filters = { ...filters, page: 1, ...patch };
      const qs = new URLSearchParams();
      if (next.userId) qs.set("member", String(next.userId));
      if (next.category) qs.set("category", next.category);
      if (next.period !== DEFAULT_PERIOD) qs.set("period", next.period);
      if (next.period === "custom") {
        if (next.from) qs.set("from", next.from);
        if (next.to) qs.set("to", next.to);
      }
      if (next.page > 1) qs.set("page", String(next.page));
      const s = qs.toString();
      // Shallow update: Next.js syncs useSearchParams without a server round trip.
      window.history.replaceState(null, "", s ? `?${s}` : window.location.pathname);
    },
    [filters],
  );

  const query = useMemo<ExpenseQuery>(() => {
    const range =
      filters.period === "custom"
        ? { from: filters.from, to: filters.to }
        : periodRange(filters.period);
    return { userId: filters.userId, category: filters.category, ...range };
  }, [filters]);

  return { filters, setFilters, query };
}
