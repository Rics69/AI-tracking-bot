import createClient from "openapi-fetch";

import { webApp } from "@/lib/telegram";

import type { components, paths } from "./schema";

// Types come from backend/api/openapi.yaml (`npm run gen:api`), so the app
// and the Go server share one contract.
export type Operation = components["schemas"]["Operation"];
export type OperationCreate = components["schemas"]["OperationCreate"];
export type OperationUpdate = components["schemas"]["OperationUpdate"];
export type Kind = components["schemas"]["Kind"];
export type Category = components["schemas"]["Category"];
export type CategoryInfo = components["schemas"]["CategoryInfo"];
export type CategoryTotal = components["schemas"]["CategoryTotal"];
export type Summary = components["schemas"]["Summary"];
export type Member = components["schemas"]["Member"];
export type Me = components["schemas"]["Me"];
export type MeUpdate = components["schemas"]["MeUpdate"];
export type Family = components["schemas"]["Family"];
export type Invite = components["schemas"]["Invite"];
export type Bucket = components["schemas"]["Bucket"];
export type TimePoint = components["schemas"]["TimePoint"];
export type SavikState = components["schemas"]["SavikState"];
export type ApiErrorBody = components["schemas"]["Error"];

// Requests go to the same origin; Next.js proxies /api/* to the Go API
// (see rewrites in next.config.ts), so no CORS is needed.
export const api = createClient<paths>({ baseUrl: "/api/v1" });

// Inside Telegram every request carries the signed init data: the API knows
// the user only from it. In a plain browser there is none, and the API serves
// the developer's account (APP_ENV=dev) or answers 401.
api.use({
  onRequest({ request }) {
    const initData = webApp()?.initData;
    if (initData) request.headers.set("Authorization", `tma ${initData}`);
    return request;
  },
});

const messages: Partial<Record<string, string>> = {
  unauthorized: "Откройте приложение из Telegram: без него нельзя понять, кто вы.",
  consent_required: "Сначала примите политику обработки данных.",
};

/** An error response of the API in the shape of the Error schema. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly body?: ApiErrorBody,
  ) {
    super(
      (body && messages[body.error.code]) ?? body?.error.message ?? `Ошибка сервера (${status})`,
    );
  }

  get field(): string | undefined {
    return this.body?.error.field;
  }

  get code(): string | undefined {
    return this.body?.error.code;
  }
}

/** Returns data of an openapi-fetch result or throws ApiError. */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || !result.response.ok) {
    throw new ApiError(result.response.status, result.error as ApiErrorBody | undefined);
  }
  return result.data as T;
}
