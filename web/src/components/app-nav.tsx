"use client";

import { WalletIcon } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";

import { cn } from "@/lib/utils";

const LINKS = [
  { href: "/", label: "Траты" },
  { href: "/family", label: "Семья" },
];

export function AppNav() {
  const pathname = usePathname();
  return (
    <header className="border-b bg-background">
      <div className="mx-auto flex h-16 w-full max-w-6xl items-center gap-8 px-4 sm:px-6">
        <Link href="/" className="flex items-center gap-2 font-semibold">
          <WalletIcon className="size-5 text-primary" />
          <span className="hidden sm:inline">Семейные расходы</span>
        </Link>
        <nav className="flex h-full gap-6">
          {LINKS.map(({ href, label }) => (
            <Link
              key={href}
              href={href}
              aria-current={pathname === href ? "page" : undefined}
              className={cn(
                "flex items-center border-b-2 border-transparent text-sm text-muted-foreground transition-colors hover:text-foreground",
                pathname === href && "border-primary font-medium text-foreground",
              )}
            >
              {label}
            </Link>
          ))}
        </nav>
      </div>
    </header>
  );
}
