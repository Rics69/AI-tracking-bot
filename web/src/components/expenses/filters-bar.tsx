"use client";

import { XIcon } from "lucide-react";

import type { CategoryInfo, User } from "@/api/client";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { toISODate } from "@/lib/format";
import { DEFAULT_PERIOD, PERIODS, type Period } from "@/lib/period";

import type { Filters } from "./use-filters";
import { memberName } from "./member";

// Radix Select does not allow an empty value, so "all" stands for "no filter".
const ALL = "all";

type Props = {
  filters: Filters;
  onChange: (patch: Partial<Filters>) => void;
  users: User[];
  categories: CategoryInfo[];
};

export function FiltersBar({ filters, onChange, users, categories }: Props) {
  const today = toISODate(new Date());
  const isDefault =
    !filters.userId && !filters.category && filters.period === DEFAULT_PERIOD;

  return (
    <div className="flex flex-wrap items-end gap-4">
      <Field label="Член семьи" id="filter-member">
        <Select
          value={filters.userId ? String(filters.userId) : ALL}
          onValueChange={(v) => onChange({ userId: v === ALL ? undefined : Number(v) })}
        >
          <SelectTrigger id="filter-member" className="h-10 w-full sm:w-52">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Все</SelectItem>
            {users.map((u) => (
              <SelectItem key={u.telegram_id} value={String(u.telegram_id)}>
                {memberName(u)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      <Field label="Период" id="filter-period">
        <Select
          value={filters.period}
          onValueChange={(v) => onChange({ period: v as Period })}
        >
          <SelectTrigger id="filter-period" className="h-10 w-full sm:w-48">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {Object.entries(PERIODS).map(([value, label]) => (
              <SelectItem key={value} value={value}>
                {label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      {filters.period === "custom" && (
        <>
          <Field label="С" id="filter-from">
            <Input
              id="filter-from"
              type="date"
              className="h-10 w-full sm:w-40"
              value={filters.from ?? ""}
              max={filters.to ?? today}
              onChange={(e) => onChange({ from: e.target.value || undefined })}
            />
          </Field>
          <Field label="По" id="filter-to">
            <Input
              id="filter-to"
              type="date"
              className="h-10 w-full sm:w-40"
              value={filters.to ?? ""}
              min={filters.from}
              max={today}
              onChange={(e) => onChange({ to: e.target.value || undefined })}
            />
          </Field>
        </>
      )}

      <Field label="Категория" id="filter-category">
        <Select
          value={filters.category ?? ALL}
          onValueChange={(v) =>
            onChange({ category: v === ALL ? undefined : (v as Filters["category"]) })
          }
        >
          <SelectTrigger id="filter-category" className="h-10 w-full sm:w-44">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>Все</SelectItem>
            {categories.map((c) => (
              <SelectItem key={c.code} value={c.code}>
                {capitalize(c.label)}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>

      {!isDefault && (
        <Button
          variant="ghost"
          className="h-10"
          onClick={() =>
            onChange({ userId: undefined, category: undefined, period: DEFAULT_PERIOD })
          }
        >
          <XIcon />
          Сбросить
        </Button>
      )}
    </div>
  );
}

function Field({ label, id, children }: { label: string; id: string; children: React.ReactNode }) {
  return (
    <div className="flex w-full flex-col gap-2 sm:w-auto">
      <Label htmlFor={id} className="text-muted-foreground">
        {label}
      </Label>
      {children}
    </div>
  );
}

export function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
