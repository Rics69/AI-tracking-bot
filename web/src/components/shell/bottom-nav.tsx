"use client";

import {
  ChartPieIcon,
  CircleUserIcon,
  HouseIcon,
  ListIcon,
  PlusIcon,
  type LucideIcon,
} from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";

import { useOperationSheet } from "@/components/operations/sheet-context";
import { cn } from "@/lib/utils";
import { haptic } from "@/lib/telegram";

const tabs: { href: string; label: string; icon: LucideIcon }[] = [
  { href: "/", label: "Главная", icon: HouseIcon },
  { href: "/history", label: "История", icon: ListIcon },
  { href: "/analytics", label: "Аналитика", icon: ChartPieIcon },
  { href: "/profile", label: "Профиль", icon: CircleUserIcon },
];

/** The app's bottom navigation with the "+" button in the middle. */
export function BottomNav() {
  const pathname = usePathname();
  const sheet = useOperationSheet();
  const tab = (t: (typeof tabs)[number]) => {
    const active = t.href === "/" ? pathname === "/" : pathname.startsWith(t.href);
    return (
      <Link
        key={t.href}
        href={t.href}
        onClick={() => !active && haptic.select()}
        aria-current={active ? "page" : undefined}
        className={cn(
          "flex min-h-12 flex-1 flex-col items-center justify-center gap-1 text-[11px] font-medium transition-colors",
          active ? "text-primary" : "text-muted-foreground",
        )}
      >
        <t.icon className="size-6" strokeWidth={active ? 2.25 : 1.75} />
        {t.label}
      </Link>
    );
  };

  return (
    <nav
      aria-label="Разделы"
      className="fixed inset-x-0 bottom-0 z-40 border-t bg-card/95 pb-[var(--safe-bottom)] backdrop-blur"
    >
      <div className="mx-auto flex h-16 max-w-lg items-center px-2">
        {tabs.slice(0, 2).map(tab)}
        <div className="flex flex-1 justify-center">
          <button
            type="button"
            aria-label="Добавить трату или доход"
            onClick={() => {
              haptic.tap();
              sheet.openNew();
            }}
            className="-mt-6 flex size-14 items-center justify-center rounded-full bg-primary text-primary-foreground shadow-md transition-transform active:scale-95"
          >
            <PlusIcon className="size-7" strokeWidth={2.5} />
          </button>
        </div>
        {tabs.slice(2).map(tab)}
      </div>
    </nav>
  );
}
