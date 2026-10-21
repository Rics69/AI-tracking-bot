"use client";

import { AlertCircleIcon, PlusIcon, RefreshCwIcon } from "lucide-react";
import { useCallback, useMemo, useState } from "react";

import type { Expense } from "@/api/client";
import { useCategories, useExpenses, useSummary, useUsers } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { PERIODS } from "@/lib/period";

import { DeleteExpenseDialog } from "./delete-expense-dialog";
import { ExpenseFormDialog } from "./expense-form-dialog";
import { ExpensesTable } from "./expenses-table";
import { FiltersBar } from "./filters-bar";
import { memberName } from "./member";
import { SpendingChart } from "./spending-chart";
import { SummaryCards } from "./summary-cards";
import { useFilters } from "./use-filters";

const PAGE_SIZE = 20;

export function Dashboard() {
  const { filters, setFilters, query } = useFilters();

  const users = useUsers();
  const categories = useCategories();
  const expenses = useExpenses(query, PAGE_SIZE, (filters.page - 1) * PAGE_SIZE);
  const summary = useSummary(query);

  // The form is open for a new expense ("new") or for an existing one.
  const [editing, setEditing] = useState<Expense | "new" | null>(null);
  const [deleting, setDeleting] = useState<Expense | undefined>();

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
      <div className="flex flex-col gap-4 lg:flex-row lg:items-end lg:justify-between">
        <FiltersBar
          filters={filters}
          onChange={setFilters}
          users={users.data ?? []}
          categories={categories.data ?? []}
        />
        <Button
          className="h-10 shrink-0 self-start lg:self-auto"
          disabled={!(users.data ?? []).length}
          title={
            (users.data ?? []).length
              ? undefined
              : "В семье пока никого нет: пригласите участников в разделе «Семья»"
          }
          onClick={() => setEditing("new")}
        >
          <PlusIcon />
          Добавить трату
        </Button>
      </div>

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
          <SpendingChart query={query} categoryLabel={categoryLabel} />
          <ExpensesTable
            items={expenses.data?.items}
            total={expenses.data?.total ?? 0}
            page={filters.page}
            pageSize={PAGE_SIZE}
            onPageChange={(page) => setFilters({ page })}
            memberLabel={memberLabel}
            categoryLabel={categoryLabel}
            stale={expenses.isPlaceholderData}
            onEdit={setEditing}
            onDelete={setDeleting}
          />
        </>
      )}

      <ExpenseFormDialog
        open={editing !== null}
        onOpenChange={(open) => !open && setEditing(null)}
        expense={editing === "new" ? undefined : (editing ?? undefined)}
        defaultUserId={filters.userId}
        users={users.data ?? []}
        categories={categories.data ?? []}
        onDelete={setDeleting}
      />
      <DeleteExpenseDialog
        expense={deleting}
        onOpenChange={(open) => !open && setDeleting(undefined)}
        memberLabel={memberLabel}
      />
    </div>
  );
}
