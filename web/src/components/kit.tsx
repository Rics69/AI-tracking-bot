"use client";

import type { LucideIcon } from "lucide-react";
import type { ReactNode } from "react";

import { cn } from "@/lib/utils";
import { haptic } from "@/lib/telegram";

// The building blocks of the app's look: white cards with a hairline border,
// icons in circles, pill badges, pill segmented controls.

export function Card({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <section className={cn("rounded-[22px] border border-border bg-card text-card-foreground shadow-[0_1px_2px_rgb(0_0_0/0.03)]", className)}>
      {children}
    </section>
  );
}

/** A card with an icon-and-title header and an optional action on the right. */
export function Panel({
  icon,
  title,
  action,
  children,
  className,
}: {
  icon: LucideIcon;
  title: string;
  action?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card className={cn("flex flex-col gap-4 p-4", className)}>
      <header className="flex min-h-10 items-center justify-between gap-3">
        <div className="flex items-center gap-3">
          <IconCircle icon={icon} />
          <h2 className="text-[17px] font-medium">{title}</h2>
        </div>
        {action}
      </header>
      {children}
    </Card>
  );
}

type Tone = "card" | "hero" | "soft";

/** An icon in a circle, tinted for the surface it sits on. */
export function IconCircle({ icon: Icon, tone = "card", className }: { icon: LucideIcon; tone?: Tone; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex size-9 shrink-0 items-center justify-center rounded-full",
        tone === "card" && "bg-muted text-foreground",
        tone === "hero" && "bg-hero-foreground/12 text-hero-foreground",
        tone === "soft" && "bg-soft-foreground/10 text-soft-foreground",
        className,
      )}
    >
      <Icon className="size-[18px]" strokeWidth={1.75} />
    </span>
  );
}

type BadgeTone = "neutral" | "hero" | "soft" | "success" | "warning" | "danger";

/** A small pill with a soft background: "3 траты", "+12% к сентябрю". */
export function Pill({ tone = "neutral", children, className }: { tone?: BadgeTone; children: ReactNode; className?: string }) {
  return (
    <span
      className={cn(
        "inline-flex h-6 items-center rounded-full px-2.5 text-[12px] font-medium whitespace-nowrap",
        tone === "neutral" && "bg-muted text-muted-foreground",
        tone === "hero" && "bg-hero-foreground/14 text-hero-foreground",
        tone === "soft" && "bg-soft-foreground/10 text-soft-foreground",
        tone === "success" && "bg-success-soft text-success",
        tone === "warning" && "bg-warning-soft text-foreground",
        tone === "danger" && "bg-danger-soft text-destructive",
        className,
      )}
    >
      {children}
    </span>
  );
}

/** A metric card: icon, label, big number, a pill underneath. */
export function StatCard({
  icon,
  label,
  value,
  pill,
  tone = "card",
  onClick,
}: {
  icon: LucideIcon;
  label: string;
  value: ReactNode;
  pill?: ReactNode;
  tone?: Tone;
  onClick?: () => void;
}) {
  const Tag = onClick ? "button" : "div";
  return (
    <Tag
      type={onClick ? "button" : undefined}
      onClick={onClick}
      className={cn(
        "flex min-h-[132px] min-w-0 flex-col justify-between gap-3 rounded-[22px] p-4 text-left",
        tone === "card" && "border border-border bg-card text-card-foreground",
        tone === "hero" && "bg-hero text-hero-foreground",
        tone === "soft" && "bg-soft text-soft-foreground",
        onClick && "transition-transform active:scale-[0.98]",
      )}
    >
      <span className="flex items-center gap-2.5">
        <IconCircle icon={icon} tone={tone} />
        <span className={cn("truncate text-[13px]", tone === "card" && "text-muted-foreground")}>{label}</span>
      </span>
      <span className="flex flex-col items-start gap-2">
        <span className="max-w-full truncate text-[26px] leading-none font-medium tracking-tight tabular-nums">{value}</span>
        {pill}
      </span>
    </Tag>
  );
}

type Option<T extends string> = { value: T; label: string };

/** A pill segmented control: a tray with the active option raised in white. */
export function Segmented<T extends string>({
  label,
  options,
  value,
  onChange,
  className,
}: {
  label: string;
  options: Option<T>[];
  value: T;
  onChange: (v: T) => void;
  className?: string;
}) {
  return (
    <div className={cn("-mx-4 overflow-x-auto px-4 [scrollbar-width:none]", className)}>
      <div role="radiogroup" aria-label={label} className="inline-flex gap-1 rounded-full bg-accent p-1">
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
              "h-9 shrink-0 rounded-full px-4 text-[14px] whitespace-nowrap transition-colors",
              o.value === value ? "bg-card font-medium text-foreground shadow-sm" : "text-muted-foreground",
            )}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}

/** "Сейвпоинт › История" over a large title. */
export function PageHeader({ section, title, action }: { section?: string; title: string; action?: ReactNode }) {
  return (
    <header className="flex items-end justify-between gap-3 px-1">
      <div className="flex flex-col gap-1">
        <p className="text-[13px] text-muted-foreground">
          Сейвпоинт <span className="px-1">›</span> <span className="text-foreground">{section ?? title}</span>
        </p>
        <h1 className="text-[32px] leading-tight font-medium tracking-tight">{title}</h1>
      </div>
      {action}
    </header>
  );
}

/** A pill button: solid ink for the main action, outlined for the rest. */
export function PillButton({
  variant = "solid",
  icon: Icon,
  children,
  className,
  ...props
}: React.ComponentProps<"button"> & { variant?: "solid" | "outline" | "soft"; icon?: LucideIcon }) {
  return (
    <button
      type="button"
      {...props}
      className={cn(
        "inline-flex h-11 shrink-0 items-center justify-center gap-2 rounded-full px-5 text-[15px] font-medium transition-[transform,opacity] active:scale-[0.98] disabled:opacity-50",
        variant === "solid" && "bg-primary text-primary-foreground",
        variant === "outline" && "border border-input bg-card text-foreground",
        variant === "soft" && "bg-soft text-soft-foreground",
        className,
      )}
    >
      {Icon && <Icon className="size-[18px]" strokeWidth={1.75} />}
      {children}
    </button>
  );
}
