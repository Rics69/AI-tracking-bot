"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, unwrap, type Category, type ExpenseCreate, type ExpenseUpdate } from "./client";

export type ExpenseQuery = {
  userId?: number;
  category?: Category;
  from?: string;
  to?: string;
};

export const queryKeys = {
  users: ["users"] as const,
  invites: ["invites"] as const,
  categories: ["categories"] as const,
  // Prefix shared by lists and summaries: invalidated together after a change.
  expenses: ["expenses"] as const,
  expenseList: (q: ExpenseQuery, limit: number, offset: number) =>
    ["expenses", "list", q, limit, offset] as const,
  expenseSummary: (q: ExpenseQuery) => ["expenses", "summary", q] as const,
};

function toParams(q: ExpenseQuery) {
  return { user_id: q.userId, category: q.category, from: q.from, to: q.to };
}

export function useUsers() {
  return useQuery({
    queryKey: queryKeys.users,
    queryFn: async () => unwrap(await api.GET("/users")).items,
    staleTime: 60_000,
  });
}

export function useCategories() {
  return useQuery({
    queryKey: queryKeys.categories,
    queryFn: async () => unwrap(await api.GET("/categories")).items,
    staleTime: Infinity, // a fixed list
  });
}

export function useExpenses(q: ExpenseQuery, limit: number, offset: number) {
  return useQuery({
    queryKey: queryKeys.expenseList(q, limit, offset),
    queryFn: async () =>
      unwrap(await api.GET("/expenses", { params: { query: { ...toParams(q), limit, offset } } })),
    // Keep showing the previous page while the next one loads: no table flicker.
    placeholderData: keepPreviousData,
  });
}

export function useSummary(q: ExpenseQuery) {
  return useQuery({
    queryKey: queryKeys.expenseSummary(q),
    queryFn: async () =>
      unwrap(await api.GET("/expenses/summary", { params: { query: toParams(q) } })),
    placeholderData: keepPreviousData,
  });
}

// Mutations invalidate every expenses query: lists and summaries for any
// filter may include the changed expense.

export function useCreateExpense() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: ExpenseCreate) => unwrap(await api.POST("/expenses", { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.expenses }),
  });
}

export function useUpdateExpense() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, body }: { id: number; body: ExpenseUpdate }) =>
      unwrap(await api.PATCH("/expenses/{id}", { params: { path: { id } }, body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.expenses }),
  });
}

export function useDeleteExpense() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) =>
      unwrap(await api.DELETE("/expenses/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.expenses }),
  });
}

export function useInvites() {
  return useQuery({
    queryKey: queryKeys.invites,
    queryFn: async () => unwrap(await api.GET("/invites")).items,
  });
}

export function useCreateInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async () => unwrap(await api.POST("/invites")),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.invites }),
  });
}

export function useRevokeInvite() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: number) =>
      unwrap(await api.DELETE("/invites/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.invites }),
  });
}

export function useSetWeeklyReport() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ telegramId, on }: { telegramId: number; on: boolean }) =>
      unwrap(
        await api.PATCH("/users/{telegram_id}", {
          params: { path: { telegram_id: telegramId } },
          body: { weekly_report: on },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.users }),
  });
}

export function useRemoveMember() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (telegramId: number) =>
      unwrap(
        await api.DELETE("/users/{telegram_id}", {
          params: { path: { telegram_id: telegramId } },
        }),
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.users }),
  });
}
