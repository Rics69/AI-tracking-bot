"use client";

import { ChartPieIcon, CircleUserIcon, HouseIcon, ListIcon, PlusIcon, type LucideIcon } from "lucide-react";
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

/** A floating capsule at the bottom: four sections and the "+" in the middle. */
export function BottomNav() {
  const pathname = usePathname();
  const sheet = useOperationSheet();
  const tab = (t: (typeof tabs)[number]) => {
    const active = t.href === "/" ? pathname === "/" : pathname.startsWith(t.href);
    return (
      <Link
        key={t.href}
        href={t.href}
        aria-label={t.label}
        aria-current={active ? "page" : undefined}
        onClick={() => !active && haptic.select()}
        className={cn(
          "flex size-12 items-center justify-center rounded-full transition-colors",
          active ? "bg-nav-foreground text-nav" : "text-nav-foreground/60",
        )}
      >
        <t.icon className="size-[22px]" strokeWidth={active ? 2 : 1.75} />
      </Link>
    );
  };

  return (
    <nav
      aria-label="Разделы"
      className="pointer-events-none fixed inset-x-0 bottom-0 z-40 flex justify-center px-4 pb-[calc(0.75rem+var(--safe-bottom))]"
    >
      <div className="pointer-events-auto flex items-center gap-1 rounded-full bg-nav p-1.5 text-nav-foreground shadow-[0_10px_30px_rgb(0_0_0/0.18)]">
        {tabs.slice(0, 2).map(tab)}
        <button
          type="button"
          aria-label="Добавить трату или доход"
          onClick={() => {
            haptic.tap();
            sheet.openNew();
          }}
          className="mx-1 flex size-12 items-center justify-center rounded-full bg-soft text-soft-foreground transition-transform active:scale-95"
        >
          <PlusIcon className="size-6" strokeWidth={2.25} />
        </button>
        {tabs.slice(2).map(tab)}
      </div>
    </nav>
  );
}
