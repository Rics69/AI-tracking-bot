import { Suspense } from "react";

import { Dashboard } from "@/components/expenses/dashboard";
import { Skeleton } from "@/components/ui/skeleton";

export default function Home() {
  return (
    <main className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 py-8 sm:px-6 lg:py-12">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">Траты</h1>
        <p className="text-muted-foreground">
          Траты всех членов семьи, записанные через Telegram-бота
        </p>
      </header>
      {/* The dashboard reads filters from the URL, which is only known in the browser. */}
      <Suspense fallback={<DashboardSkeleton />}>
        <Dashboard />
      </Suspense>
    </main>
  );
}

function DashboardSkeleton() {
  return (
    <div className="flex flex-col gap-8" aria-busy>
      <Skeleton className="h-16 w-full max-w-3xl" />
      <Skeleton className="h-40 w-full" />
      <Skeleton className="h-72 w-full" />
    </div>
  );
}
