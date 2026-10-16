"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";

import { api, unwrap, type Category } from "./client";

export type ExpenseQuery = {
  userId?: number;
  category?: Category;
  from?: string;
  to?: string;
};

export const queryKeys = {
  users: ["users"] as const,
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
    queryFn: async () => unwrap(await api.GET("/expenses/summary", { params: { query: toParams(q) } })),
    placeholderData: keepPreviousData,
  });
}
