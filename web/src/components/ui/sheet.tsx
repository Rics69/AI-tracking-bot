"use client";

import { Dialog as DialogPrimitive } from "radix-ui";
import { useEffect, type ReactNode } from "react";

import { cn } from "@/lib/utils";
import { webApp } from "@/lib/telegram";

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  /** Hidden from sight, read by screen readers. */
  description?: string;
  children: ReactNode;
  className?: string;
};

/**
 * A bottom sheet: the phone-native way to add or edit something without
 * leaving the screen. In Telegram its Back button closes the sheet.
 */
export function Sheet({ open, onOpenChange, title, description, children, className }: Props) {
  useTelegramBack(open, () => onOpenChange(false));
  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className="fixed inset-0 z-50 bg-black/40 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0" />
        <DialogPrimitive.Content
          className={cn(
            "fixed inset-x-0 bottom-0 z-50 mx-auto flex max-h-[92dvh] w-full max-w-lg flex-col rounded-t-3xl bg-popover text-popover-foreground shadow-lg outline-none",
            "pb-[calc(1rem+var(--safe-bottom))] data-[state=closed]:animate-out data-[state=closed]:slide-out-to-bottom data-[state=open]:animate-in data-[state=open]:slide-in-from-bottom data-[state=open]:duration-300",
            className,
          )}
        >
          <div
            aria-hidden
            className="mx-auto mt-2 mb-1 h-1 w-10 shrink-0 rounded-full bg-muted-foreground/30"
          />
          <DialogPrimitive.Title className="px-5 pt-2 pb-1 text-lg font-semibold">
            {title}
          </DialogPrimitive.Title>
          <DialogPrimitive.Description className="sr-only">
            {description ?? title}
          </DialogPrimitive.Description>
          <div className="overflow-y-auto px-5 pt-3">{children}</div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}

/** Shows Telegram's Back button while active; pressing it calls onBack. */
export function useTelegramBack(active: boolean, onBack: () => void) {
  useEffect(() => {
    const back = webApp()?.BackButton;
    if (!active || !back) return;
    back.show();
    back.onClick(onBack);
    return () => {
      back.offClick(onBack);
      back.hide();
    };
  }, [active, onBack]);
}
