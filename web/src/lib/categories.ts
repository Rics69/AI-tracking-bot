"use client";

import {
  BriefcaseIcon,
  CarIcon,
  ClapperboardIcon,
  CoinsIcon,
  EllipsisIcon,
  GiftIcon,
  HeartPulseIcon,
  HouseIcon,
  LaptopIcon,
  PercentIcon,
  ShoppingBagIcon,
  Undo2Icon,
  UtensilsIcon,
  type LucideIcon,
} from "lucide-react";
import { useMemo } from "react";

import type { Category, CategoryInfo, Kind } from "@/api/client";
import { useCategories } from "@/api/queries";

export const categoryIcons: Record<Category, LucideIcon> = {
  food: UtensilsIcon,
  transport: CarIcon,
  housing: HouseIcon,
  entertainment: ClapperboardIcon,
  health: HeartPulseIcon,
  shopping: ShoppingBagIcon,
  other: EllipsisIcon,
  salary: BriefcaseIcon,
  freelance: LaptopIcon,
  gift: GiftIcon,
  interest: PercentIcon,
  refund: Undo2Icon,
  other_income: CoinsIcon,
};

/** Categories from the API: labels and the lists per kind, in display order. */
export function useCategoryInfo() {
  const categories = useCategories();
  return useMemo(() => {
    const items = categories.data ?? [];
    const labels = new Map(items.map((c) => [c.code, c.label]));
    const byKind: Record<Kind, CategoryInfo[]> = {
      expense: items.filter((c) => c.kind === "expense"),
      income: items.filter((c) => c.kind === "income"),
    };
    return {
      ready: categories.isSuccess,
      label: (c: Category) => labels.get(c) ?? c,
      byKind,
    };
  }, [categories.data, categories.isSuccess]);
}

/** "еда" → "Еда". */
export function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
