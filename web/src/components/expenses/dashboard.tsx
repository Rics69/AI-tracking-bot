"use client";

import { AlertCircleIcon, RefreshCwIcon } from "lucide-react";
import { useCallback, useMemo } from "react";

import { useCategories, useExpenses, useSummary, useUsers } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { PERIODS } from "@/lib/period";

import { ExpensesTable } from "./expenses-table";
import { FiltersBar } from "./filters-bar";
import { memberName } from "./member";
import { SummaryCards } from "./summary-cards";
import { useFilters } from "./use-filters";

const PAGE_SIZE = 20;

export function Dashboard() {
  const { filters, setFilters, query } = useFilters();

  const users = useUsers();
  const categories = useCategories();
  const expenses = useExpenses(query, PAGE_SIZE, (filters.page - 1) * PAGE_SIZE);
  const summary = useSummary(query);

  const memberLabel = useMemo(() => {
    const names = new Map(users.data?.map((u) => [u.telegram_id, memberName(u)]));
    return (id: number) => names.get(id) ?? `ID ${id}`;
  }, [users.data]);

  const categoryLabel = useMemo(() => {
    const labels = new Map(categories.data?.map((c) => [c.code as string, c.label]));
    return (code: string) => labels.get(code) ?? code;
  }, [categories.data]);

  const retry = useCallback(() => {
    void expenses.refetch();
    void summary.refetch();
  }, [expenses, summary]);

  const error = expenses.error ?? summary.error;

  return (
    <div className="flex flex-col gap-8">
      <FiltersBar
        filters={filters}
        onChange={setFilters}
        users={users.data ?? []}
        categories={categories.data ?? []}
      />

      {error ? (
        <div
          role="alert"
          className="flex flex-col items-start gap-4 rounded-xl p-6 ring-1 ring-destructive/30 sm:flex-row sm:items-center sm:justify-between"
        >
          <div className="flex items-center gap-3">
            <AlertCircleIcon className="size-5 shrink-0 text-destructive" />
            <div>
              <p className="font-medium">Не удалось загрузить траты</p>
              <p className="text-sm text-muted-foreground">{error.message}</p>
            </div>
          </div>
          <Button variant="outline" className="h-10" onClick={retry}>
            <RefreshCwIcon />
            Повторить
          </Button>
        </div>
      ) : (
        <>
          <SummaryCards
            summary={summary.data}
            categoryLabel={categoryLabel}
            periodLabel={PERIODS[filters.period]}
          />
          <ExpensesTable
            items={expenses.data?.items}
            total={expenses.data?.total ?? 0}
            page={filters.page}
            pageSize={PAGE_SIZE}
            onPageChange={(page) => setFilters({ page })}
            memberLabel={memberLabel}
            categoryLabel={categoryLabel}
            stale={expenses.isPlaceholderData}
          />
        </>
      )}
    </div>
  );
}
