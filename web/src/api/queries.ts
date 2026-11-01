"use client";

import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import {
  api,
  unwrap,
  type AccountCreate,
  type AccountUpdate,
  type Bucket,
  type CardRequest,
  type Category,
  type ClientEvent,
  type GoalCreate,
  type GoalUpdate,
  type Kind,
  type MeUpdate,
  type OperationCreate,
  type OperationUpdate,
  type PlanInput,
  type RegularPaymentCreate,
  type RegularPaymentUpdate,
  type TaskCreate,
  type TransferCreate,
} from "./client";

/** What the history and charts show. */
export type OperationQuery = {
  kind?: Kind;
  userId?: number;
  category?: Category;
  from?: string;
  to?: string;
};

// Every query about money starts with "money": a new or changed operation
// invalidates them all at once (lists, totals, charts, Savik).
export const keys = {
  me: ["me"] as const,
  family: ["family"] as const,
  categories: ["categories"] as const,
  money: ["money"] as const,
  operations: (q: OperationQuery) => ["money", "operations", q] as const,
  recent: ["money", "recent"] as const,
  summary: (q: OperationQuery) => ["money", "summary", q] as const,
  series: (q: OperationQuery, bucket: Bucket) => ["money", "series", q, bucket] as const,
  savik: ["money", "savik"] as const,
  // The limit, goals and payments depend on operations too: also under "money".
  budget: ["money", "budget"] as const,
  goals: ["money", "goals"] as const,
  payments: ["money", "payments"] as const,
  // Balances move with every operation, and a new operation may close a quest.
  accounts: ["money", "accounts"] as const,
  transfers: ["money", "transfers"] as const,
  game: ["money", "game"] as const,
  rates: ["rates"] as const,
  tasks: (from: string, to: string) => ["tasks", from, to] as const,
  allTasks: ["tasks"] as const,
};

function toParams(q: OperationQuery) {
  return { kind: q.kind, user_id: q.userId, category: q.category, from: q.from, to: q.to };
}

// Me.

export function useMe() {
  return useQuery({
    queryKey: keys.me,
    queryFn: async () => unwrap(await api.GET("/me")),
    staleTime: 60_000,
    retry: false, // 401 outside Telegram is an answer, not a glitch
  });
}

export function useUpdateMe() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (body: MeUpdate) => unwrap(await api.PATCH("/me", { body })),
    onSuccess: (me) => qc.setQueryData(keys.me, me),
  });
}

/** Fire and forget: statistics never bother the user. */
export function trackEvent(body: ClientEvent) {
  void api.POST("/events", { body }).catch(() => undefined);
}

export function useDeleteMe() {
  return useMutation({ mutationFn: async () => unwrap(await api.DELETE("/me")) });
}

export function useCategories() {
  return useQuery({
    queryKey: keys.categories,
    queryFn: async () => unwrap(await api.GET("/categories")).items,
    staleTime: Infinity, // a fixed list
  });
}

// Money.

const PAGE = 30;

/** Operations, newest first, loaded page by page as the list scrolls. */
export function useOperations(q: OperationQuery) {
  return useInfiniteQuery({
    queryKey: keys.operations(q),
    initialPageParam: 0,
    queryFn: async ({ pageParam }) =>
      unwrap(
        await api.GET("/operations", {
          params: { query: { ...toParams(q), limit: PAGE, offset: pageParam } },
        }),
      ),
    getNextPageParam: (last, pages) => {
      const loaded = pages.reduce((n, p) => n + p.items.length, 0);
      return loaded < last.total ? loaded : undefined;
    },
    placeholderData: keepPreviousData,
  });
}

export function useRecentOperations(limit = 5) {
  return useQuery({
    queryKey: keys.recent,
    queryFn: async () =>
      unwrap(await api.GET("/operations", { params: { query: { limit } } })).items,
  });
}

export function useSummary(q: OperationQuery) {
  return useQuery({
    queryKey: keys.summary(q),
    queryFn: async () => unwrap(await api.GET("/summary", { params: { query: toParams(q) } })),
    placeholderData: keepPreviousData,
  });
}

export function useTimeSeries(q: OperationQuery & { kind: Kind }, bucket: Bucket) {
  return useQuery({
    queryKey: keys.series(q, bucket),
    queryFn: async () =>
      unwrap(
        await api.GET("/timeseries", {
          params: { query: { ...toParams(q), kind: q.kind, bucket } },
        }),
      ),
    placeholderData: keepPreviousData,
  });
}

export function useSavik() {
  return useQuery({
    queryKey: keys.savik,
    queryFn: async () => unwrap(await api.GET("/savik")),
  });
}

function useMoneyMutation<T, R>(fn: (arg: T) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.money }),
  });
}

export function useCreateOperation() {
  return useMoneyMutation(async (body: OperationCreate) =>
    unwrap(await api.POST("/operations", { body })),
  );
}

export function useUpdateOperation() {
  return useMoneyMutation(async ({ id, body }: { id: number; body: OperationUpdate }) =>
    unwrap(await api.PATCH("/operations/{id}", { params: { path: { id } }, body })),
  );
}

export function useDeleteOperation() {
  return useMoneyMutation(async (id: number) =>
    unwrap(await api.DELETE("/operations/{id}", { params: { path: { id } } })),
  );
}

// Limit, goals and regular payments: personal, even for a family head.

export function useBudget() {
  return useQuery({
    queryKey: keys.budget,
    queryFn: async () => unwrap(await api.GET("/budget")),
  });
}

export function useGoals() {
  return useQuery({
    queryKey: keys.goals,
    queryFn: async () => unwrap(await api.GET("/goals")).items,
  });
}

export function useRegularPayments() {
  return useQuery({
    queryKey: keys.payments,
    queryFn: async () => unwrap(await api.GET("/regular-payments")).items,
  });
}

export function useSetPlan() {
  return useMoneyMutation(async (body: PlanInput) => unwrap(await api.PUT("/budget/plan", { body })));
}

export function useDeletePlan() {
  return useMoneyMutation(async () => unwrap(await api.DELETE("/budget/plan")));
}

export function useCreateGoal() {
  return useMoneyMutation(async (body: GoalCreate) => unwrap(await api.POST("/goals", { body })));
}

export function useUpdateGoal() {
  return useMoneyMutation(async ({ id, body }: { id: number; body: GoalUpdate }) =>
    unwrap(await api.PATCH("/goals/{id}", { params: { path: { id } }, body })),
  );
}

export function useDeleteGoal() {
  return useMoneyMutation(async (id: number) =>
    unwrap(await api.DELETE("/goals/{id}", { params: { path: { id } } })),
  );
}

/** Puts money aside for a goal; a negative amount takes it back. */
export function useDeposit() {
  return useMoneyMutation(async ({ id, amount }: { id: number; amount: number }) =>
    unwrap(await api.POST("/goals/{id}/deposits", { params: { path: { id } }, body: { amount } })),
  );
}

export function useCreatePayment() {
  return useMoneyMutation(async (body: RegularPaymentCreate) =>
    unwrap(await api.POST("/regular-payments", { body })),
  );
}

export function useUpdatePayment() {
  return useMoneyMutation(async ({ id, body }: { id: number; body: RegularPaymentUpdate }) =>
    unwrap(await api.PATCH("/regular-payments/{id}", { params: { path: { id } }, body })),
  );
}

export function useDeletePayment() {
  return useMoneyMutation(async (id: number) =>
    unwrap(await api.DELETE("/regular-payments/{id}", { params: { path: { id } } })),
  );
}

/** Records today's expense for a regular payment. */
export function usePayPayment() {
  return useMoneyMutation(async (id: number) =>
    unwrap(await api.POST("/regular-payments/{id}/pay", { params: { path: { id } } })),
  );
}

// Family.

export function useFamily() {
  return useQuery({
    queryKey: keys.family,
    queryFn: async () => unwrap(await api.GET("/family")),
  });
}

/** A family change can change roles (me) and whose money the head sees. */
function useFamilyMutation<T, R>(fn: (arg: T) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () =>
      Promise.all([
        qc.invalidateQueries({ queryKey: keys.family }),
        qc.invalidateQueries({ queryKey: keys.me }),
        qc.invalidateQueries({ queryKey: keys.money }),
      ]),
  });
}

export function useCreateInvite() {
  return useFamilyMutation(async () => unwrap(await api.POST("/family/invites")));
}

export function useRevokeInvite() {
  return useFamilyMutation(async (id: number) =>
    unwrap(await api.DELETE("/family/invites/{id}", { params: { path: { id } } })),
  );
}

export function useRemoveMember() {
  return useFamilyMutation(async (telegramId: number) =>
    unwrap(
      await api.DELETE("/family/members/{telegram_id}", {
        params: { path: { telegram_id: telegramId } },
      }),
    ),
  );
}

export function useSetMemberWeeklyReport() {
  return useFamilyMutation(async ({ telegramId, on }: { telegramId: number; on: boolean }) =>
    unwrap(
      await api.PATCH("/family/members/{telegram_id}", {
        params: { path: { telegram_id: telegramId } },
        body: { weekly_report: on },
      }),
    ),
  );
}

export function useLeaveFamily() {
  return useFamilyMutation(async () => unwrap(await api.POST("/family/leave")));
}

export function useTransferHead() {
  return useFamilyMutation(async (telegramId: number) =>
    unwrap(await api.PUT("/family/head", { body: { telegram_id: telegramId } })),
  );
}

// Accounts.

export function useAccounts() {
  return useQuery({
    queryKey: keys.accounts,
    queryFn: async () => unwrap(await api.GET("/accounts")),
  });
}

export function useCreateAccount() {
  return useMoneyMutation(async (body: AccountCreate) => unwrap(await api.POST("/accounts", { body })));
}

export function useUpdateAccount() {
  return useMoneyMutation(async ({ id, body }: { id: number; body: AccountUpdate }) =>
    unwrap(await api.PATCH("/accounts/{id}", { params: { path: { id } }, body })),
  );
}

export function useTransfers() {
  return useQuery({
    queryKey: keys.transfers,
    queryFn: async () => unwrap(await api.GET("/transfers")),
  });
}

export function useCreateTransfer() {
  return useMoneyMutation(async (body: TransferCreate) => unwrap(await api.POST("/transfers", { body })));
}

export function useDeleteTransfer() {
  return useMoneyMutation(async (id: number) =>
    unwrap(await api.DELETE("/transfers/{id}", { params: { path: { id } } })),
  );
}

export function useRates() {
  return useQuery({
    queryKey: keys.rates,
    queryFn: async () => unwrap(await api.GET("/rates")),
    staleTime: 10 * 60_000,
    retry: false, // the widget is optional: without rates the screen stays as it is
  });
}

// Calendar.

function useTaskMutation<T, R>(fn: (arg: T) => Promise<R>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.allTasks }),
  });
}

export function useTasks(from: string, to: string) {
  return useQuery({
    queryKey: keys.tasks(from, to),
    queryFn: async () => unwrap(await api.GET("/tasks", { params: { query: { from, to } } })),
    placeholderData: keepPreviousData,
  });
}

export function useCreateTask() {
  return useTaskMutation(async (body: TaskCreate) => unwrap(await api.POST("/tasks", { body })));
}

export function useDoneTask() {
  return useTaskMutation(async (id: number) =>
    unwrap(await api.POST("/tasks/{id}/done", { params: { path: { id } } })),
  );
}

export function useDeleteTask() {
  return useTaskMutation(async (id: number) =>
    unwrap(await api.DELETE("/tasks/{id}", { params: { path: { id } } })),
  );
}

// The game.

export function useGame() {
  return useQuery({
    queryKey: keys.game,
    queryFn: async () => unwrap(await api.GET("/game")),
  });
}

export function useDoneQuest() {
  return useMoneyMutation(async (slug: string) =>
    unwrap(await api.POST("/quests/{slug}/done", { params: { path: { slug } } })),
  );
}

export async function createCard(body: CardRequest) {
  return unwrap(await api.POST("/cards", { body }));
}
