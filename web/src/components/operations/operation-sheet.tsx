"use client";

import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { ApiError, type Category, type Kind } from "@/api/client";
import { useCreateOperation, useDeleteOperation, useUpdateOperation } from "@/api/queries";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Sheet } from "@/components/ui/sheet";
import { capitalize, categoryIcons, useCategoryInfo } from "@/lib/categories";
import { formatMoney, toISODate } from "@/lib/format";
import { cn } from "@/lib/utils";
import { haptic, webApp } from "@/lib/telegram";

import type { SheetTarget } from "./sheet-context";

type Props = {
  target: SheetTarget | null;
  onClose: () => void;
  onSaved: () => void;
};

export function OperationSheet({ target, onClose, onSaved }: Props) {
  const editing = target?.mode === "edit";
  return (
    <Sheet
      open={target !== null}
      onOpenChange={(open) => !open && onClose()}
      title={editing ? "Изменить" : "Новая запись"}
      description="Сумма, категория, дата и комментарий"
    >
      {/* Remounted on every open, so the form starts from the current target. */}
      {target && (
        <OperationForm key={formKey(target)} target={target} onClose={onClose} onSaved={onSaved} />
      )}
    </Sheet>
  );
}

function formKey(t: SheetTarget) {
  return t.mode === "edit" ? `edit-${t.operation.id}` : `new-${t.kind}`;
}

type Fields = {
  kind: Kind;
  amount: string;
  category: Category | "";
  date: string;
  comment: string;
};
type Errors = Partial<Record<"amount" | "category" | "date" | "comment", string>>;

function OperationForm({
  target,
  onClose,
  onSaved,
}: {
  target: SheetTarget;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [{ today, yesterday }] = useState(() => {
    const now = new Date();
    return {
      today: toISODate(now),
      yesterday: toISODate(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)),
    };
  });
  const op = target.mode === "edit" ? target.operation : undefined;
  const [f, setF] = useState<Fields>(() =>
    op
      ? {
          kind: op.kind,
          amount: String(op.amount).replace(".", ","),
          category: op.category,
          date: op.date,
          comment: op.comment ?? "",
        }
      : {
          kind: target.mode === "new" ? target.kind : "expense",
          amount: "",
          category: "",
          date: today,
          comment: "",
        },
  );
  const [errors, setErrors] = useState<Errors>({});
  const categories = useCategoryInfo();
  const create = useCreateOperation();
  const update = useUpdateOperation();
  const remove = useDeleteOperation();
  const pending = create.isPending || update.isPending || remove.isPending;

  const set = <K extends keyof Fields>(key: K, value: Fields[K]) => {
    setF((prev) => ({ ...prev, [key]: value }));
    setErrors((e) => ({ ...e, [key]: undefined }));
  };

  const switchKind = (kind: Kind) => {
    if (kind === f.kind) return;
    haptic.select();
    // A category belongs to one kind: switching starts the choice over.
    setF((prev) => ({ ...prev, kind, category: "" }));
  };

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const normalized = f.amount.replace(/\s/g, "").replace(",", ".");
    const amount = Number(normalized);
    const found: Errors = {};
    if (!normalized) found.amount = "Введите сумму";
    else if (!/^\d+(\.\d{1,2})?$/.test(normalized) || amount <= 0)
      found.amount = "Положительное число, до 2 знаков после запятой";
    if (!f.category) found.category = "Выберите категорию";
    if (!f.date) found.date = "Укажите дату";
    else if (f.date > today) found.date = "Дата не может быть в будущем";
    setErrors(found);
    if (Object.keys(found).length > 0) {
      haptic.error();
      return;
    }

    const body = {
      amount,
      category: f.category as Category,
      date: f.date,
      comment: f.comment.trim(),
    };
    try {
      if (op) await update.mutateAsync({ id: op.id, body });
      else await create.mutateAsync(body);
      haptic.success();
      const what = body.comment || capitalize(categories.label(body.category));
      toast.success(`${op ? "Изменено" : "Записано"}: ${what} — ${formatMoney(amount)}`);
      onSaved();
      onClose();
    } catch (err) {
      haptic.error();
      if (err instanceof ApiError && err.field && err.field in fieldNames) {
        setErrors({ [err.field]: err.message });
      } else {
        toast.error(err instanceof Error ? err.message : "Не удалось сохранить");
      }
    }
  }

  async function onDelete() {
    if (!op || !(await confirm("Удалить запись? Это нельзя отменить."))) return;
    try {
      await remove.mutateAsync(op.id);
      haptic.success();
      toast.success("Удалено");
      onClose();
    } catch (err) {
      haptic.error();
      toast.error(err instanceof Error ? err.message : "Не удалось удалить");
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-5">
      <div
        role="radiogroup"
        aria-label="Что записываем"
        className="grid grid-cols-2 gap-1 rounded-xl bg-muted p-1"
      >
        {(["expense", "income"] as const).map((k) => (
          <button
            key={k}
            type="button"
            role="radio"
            aria-checked={f.kind === k}
            onClick={() => switchKind(k)}
            className={cn(
              "h-10 rounded-lg text-[15px] font-medium transition-colors",
              f.kind === k ? "bg-card text-foreground shadow-sm" : "text-muted-foreground",
            )}
          >
            {k === "expense" ? "Расход" : "Доход"}
          </button>
        ))}
      </div>

      <div className="flex flex-col items-center gap-1">
        <label htmlFor="op-amount" className="sr-only">
          Сумма в рублях
        </label>
        <div className="flex items-baseline justify-center gap-2">
          <input
            id="op-amount"
            inputMode="decimal"
            autoComplete="off"
            autoFocus={!op}
            placeholder="0"
            value={f.amount}
            onChange={(e) => set("amount", e.target.value.replace(/[^\d\s,.]/g, ""))}
            aria-invalid={!!errors.amount}
            className="w-full max-w-60 bg-transparent text-center text-5xl font-semibold tracking-tight tabular-nums outline-none placeholder:text-muted-foreground/40"
          />
          <span className="text-3xl font-medium text-muted-foreground">₽</span>
        </div>
        <FieldError text={errors.amount} />
      </div>

      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium text-muted-foreground">Категория</legend>
        <div className="grid grid-cols-4 gap-2">
          {categories.byKind[f.kind].map((c) => {
            const Icon = categoryIcons[c.code];
            const selected = f.category === c.code;
            return (
              <button
                key={c.code}
                type="button"
                aria-pressed={selected}
                onClick={() => {
                  haptic.select();
                  set("category", c.code);
                }}
                className={cn(
                  "flex min-h-[72px] flex-col items-center justify-center gap-1.5 rounded-xl px-1 py-2 text-[13px] leading-tight transition-colors",
                  selected ? "bg-primary text-primary-foreground" : "bg-muted text-foreground",
                )}
              >
                <Icon className="size-5" />
                <span className="line-clamp-2 text-center">{capitalize(c.label)}</span>
              </button>
            );
          })}
        </div>
        <FieldError text={errors.category} />
      </fieldset>

      <div className="flex flex-col gap-2">
        <span className="text-sm font-medium text-muted-foreground">Дата</span>
        <div className="flex gap-2">
          <Chip active={f.date === today} onClick={() => set("date", today)}>
            Сегодня
          </Chip>
          <Chip active={f.date === yesterday} onClick={() => set("date", yesterday)}>
            Вчера
          </Chip>
          <input
            type="date"
            aria-label="Другая дата"
            max={today}
            value={f.date}
            onChange={(e) => set("date", e.target.value)}
            className={cn(
              "h-10 min-w-0 flex-1 rounded-full bg-muted px-3 text-[15px] outline-none",
              f.date !== today && f.date !== yesterday && "ring-2 ring-primary",
            )}
          />
        </div>
        <FieldError text={errors.date} />
      </div>

      <div className="flex flex-col gap-2">
        <label htmlFor="op-comment" className="text-sm font-medium text-muted-foreground">
          Комментарий
        </label>
        <Input
          id="op-comment"
          placeholder={f.kind === "expense" ? "Кофе, такси до работы…" : "Зарплата, заказ…"}
          maxLength={500}
          value={f.comment}
          onChange={(e) => set("comment", e.target.value)}
          className="h-11 text-[15px]"
        />
        <FieldError text={errors.comment} />
      </div>

      <div className="flex flex-col gap-2 pb-2">
        <Button type="submit" size="lg" className="h-12 text-base" disabled={pending}>
          {pending ? "Сохранение…" : op ? "Сохранить" : "Записать"}
        </Button>
        {op && (
          <Button
            type="button"
            variant="ghost"
            className="h-11 text-destructive"
            disabled={pending}
            onClick={() => void onDelete()}
          >
            Удалить
          </Button>
        )}
      </div>
    </form>
  );
}

const fieldNames = { amount: 1, category: 1, date: 1, comment: 1 };

function Chip({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "h-10 shrink-0 rounded-full px-4 text-[15px] transition-colors",
        active ? "bg-primary text-primary-foreground" : "bg-muted text-foreground",
      )}
    >
      {children}
    </button>
  );
}

function FieldError({ text }: { text?: string }) {
  if (!text) return null;
  return (
    <p role="alert" className="text-sm text-destructive">
      {text}
    </p>
  );
}

/** Telegram's native confirm inside Telegram, the browser's outside. */
export function confirm(message: string): Promise<boolean> {
  const app = webApp();
  if (app?.showConfirm && app.isVersionAtLeast("6.2")) {
    return new Promise((resolve) => app.showConfirm!(message, resolve));
  }
  return Promise.resolve(window.confirm(message));
}
