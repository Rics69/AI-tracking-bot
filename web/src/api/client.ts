import createClient from "openapi-fetch";

import type { components, paths } from "./schema";

// Types come from backend/api/openapi.yaml (`npm run gen:api`), so the panel
// and the Go server share one contract.
export type Expense = components["schemas"]["Expense"];
export type ExpenseCreate = components["schemas"]["ExpenseCreate"];
export type ExpenseUpdate = components["schemas"]["ExpenseUpdate"];
export type Category = components["schemas"]["Category"];
export type CategoryInfo = components["schemas"]["CategoryInfo"];
export type User = components["schemas"]["User"];
export type Summary = components["schemas"]["Summary"];
export type ApiErrorBody = components["schemas"]["Error"];

// Requests go to the same origin; Next.js proxies /api/* to the Go API
// (see rewrites in next.config.ts), so no CORS is needed.
export const api = createClient<paths>({ baseUrl: "/api/v1" });

/** An error response of the API in the shape of the Error schema. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly body?: ApiErrorBody,
  ) {
    super(body?.error.message ?? `Ошибка сервера (${status})`);
  }

  get field(): string | undefined {
    return this.body?.error.field;
  }
}

/** Returns data of an openapi-fetch result or throws ApiError. */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || !result.response.ok) {
    throw new ApiError(result.response.status, result.error as ApiErrorBody | undefined);
  }
  return result.data as T;
}
