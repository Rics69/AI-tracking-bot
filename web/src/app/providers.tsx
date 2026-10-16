"use client";

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

let browserQueryClient: QueryClient | undefined;

function makeQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: 1,
        // Family members add expenses through the bot while the panel is open:
        // refresh when the tab regains focus and every 30 seconds.
        refetchOnWindowFocus: true,
        refetchInterval: 30_000,
      },
    },
  });
}

function getQueryClient() {
  // A fresh client per server render, one shared client in the browser.
  if (typeof window === "undefined") return makeQueryClient();
  browserQueryClient ??= makeQueryClient();
  return browserQueryClient;
}

export function Providers({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={getQueryClient()}>{children}</QueryClientProvider>;
}
