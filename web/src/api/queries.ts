"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  api,
  unwrap,
  type Bucket,
  type Category,
  type ExpenseCreate,
  type ExpenseUpdate,
  type Summary,
} from "./client";

export type ExpenseQuery = {
  userId?: number;
  category?: Category;
  from?: string;
  to?: string;
};

export const queryKeys = {
  me: ["me"] as const,
  family: ["family"] as const,
  categories: ["categories"] as const,
  // Prefix shared by lists and summaries: invalidated together after a change.
  expenses: ["expenses"] as const,
  expenseList: (q: ExpenseQuery, limit: number, offset: number) =>
    ["expenses", "list", q, limit, offset] as const,
  expenseSummary: (q: ExpenseQuery) => ["expenses", "summary", q] as const,
  expenseSeries: (q: ExpenseQuery, bucket: Bucket) => ["expenses", "series", q, bucket] as const,
};

function toParams(q: ExpenseQuery) {
  return { user_id: q.userId, category: q.category, from: q.from, to: q.to };
}

export function useMe() {
  return useQuery({
    queryKey: queryKeys.me,
    queryFn: async () => unwrap(await api.GET("/me")),
    staleTime: 60_000,
  });
}

function useFamily<T>(select: (f: Awaited<ReturnType<typeof fetchFamily>>) => T) {
  return useQuery({ queryKey: queryKeys.family, queryFn: fetchFamily, staleTime: 60_000, select });
}

async function fetchFamily() {
  return unwrap(await api.GET("/family"));
}

/** Family members, the head first. */
export function useUsers() {
  return useFamily((f) => f.members);
}

/** Invites of the last month: the head sees them, a member gets none. */
export function useInvites() {
  return useFamily((f) => f.invites);
}

/** Expense categories: the panel does not show incomes. */
export function useCategories() {
  return useQuery({
    queryKey: queryKeys.categories,
    queryFn: async () => unwrap(await api.GET("/categories")).items,
    staleTime: Infinity, // a fixed list
    select: (items) => items.filter((c) => c.kind === "expense"),
  });
}

export function useExpenses(q: ExpenseQuery, limit: number, offset: number) {
  return useQuery({
    queryKey: queryKeys.expenseList(q, limit, offset),
    queryFn: async () =>
      unwrap(
        await api.GET("/operations", {
          params: { query: { ...toParams(q), kind: "expense", limit, offset } },
        }),
      ),
    // Keep showing the previous page while the next one loads: no table flicker.
    placeholderData: keepPreviousData,
  });
}

export function useSummary(q: ExpenseQuery) {
  return useQuery({
    queryKey: queryKeys.expenseSummary(q),
    queryFn: async (): Promise<Summary> => {
      const s = unwrap(await api.GET("/summary", { params: { query: toParams(q) } }));
      return { items: s.expense.categories, sum: s.expense.sum, count: s.expense.count };
    },
    placeholderData: keepPreviousData,
  });
}

// Mutations invalidate every expenses query: lists and summaries for any
// filter may include the changed expense.

export function useCreateExpense() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: ExpenseCreate) => unwrap(await api.POST("/operations", { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.expenses }),
  });
}

export function useUpdateExpense() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: { id: number; body: ExpenseUpdate }) =>
      unwrap(await api.PATCH("/operations/{id}", { params: { path: { id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.expenses }),
  });
}

export function useDeleteExpense() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) =>
      unwrap(await api.DELETE("/operations/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.expenses }),
  });
}

export function useTimeSeries(q: ExpenseQuery, bucket: Bucket) {
  return useQuery({
    queryKey: queryKeys.expenseSeries(q, bucket),
    queryFn: async () =>
      unwrap(
        await api.GET("/timeseries", {
          params: { query: { ...toParams(q), bucket, kind: "expense" } },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useCreateInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await api.POST("/family/invites")),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.family }),
  });
}

export function useRevokeInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) =>
      unwrap(await api.DELETE("/family/invites/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.family }),
  });
}

export function useSetWeeklyReport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ telegramId, on }: { telegramId: number; on: boolean }) =>
      unwrap(
        await api.PATCH("/family/members/{telegram_id}", {
          params: { path: { telegram_id: telegramId } },
          body: { weekly_report: on },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.family }),
  });
}

export function useRemoveMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (telegramId: number) =>
      unwrap(
        await api.DELETE("/family/members/{telegram_id}", {
          params: { path: { telegram_id: telegramId } },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.family }),
  });
}
