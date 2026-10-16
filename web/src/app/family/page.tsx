import type { Metadata } from "next";

import { FamilyPage } from "@/components/family/family-page";

export const metadata: Metadata = { title: "Семья · Семейные расходы" };

export default function Family() {
  return (
    <main className="mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 py-8 sm:px-6 lg:py-12">
      <header className="flex flex-col gap-2">
        <h1 className="text-3xl font-semibold tracking-tight">Семья</h1>
        <p className="text-muted-foreground">
          Кто записывает траты через бота. Новые участники присоединяются по ссылке-приглашению.
        </p>
      </header>
      <FamilyPage />
    </main>
  );
}
