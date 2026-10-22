"use client";

import { cn } from "@/lib/utils";
import { haptic } from "@/lib/telegram";

type Option<T extends string> = { value: T; label: string };

/** A horizontally scrolling row of single-choice chips. */
export function Chips<T extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: Option<T>[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-1 [scrollbar-width:none]"
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          onClick={() => {
            if (o.value === value) return;
            haptic.select();
            onChange(o.value);
          }}
          className={cn(
            "h-9 shrink-0 rounded-full px-4 text-[15px] whitespace-nowrap transition-colors",
            o.value === value ? "bg-primary text-primary-foreground" : "bg-card text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
