"use client";

import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { ApiError, type Category, type CategoryInfo, type Expense, type User } from "@/api/client";
import { useCreateExpense, useUpdateExpense } from "@/api/queries";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatMoney, toISODate } from "@/lib/format";

import { capitalize } from "./filters-bar";
import { memberName } from "./member";

type Fields = {
  userId: string;
  amount: string;
  category: Category | "";
  spentAt: string;
  comment: string;
};

type FieldName = keyof Fields;
type Errors = Partial<Record<FieldName, string>>;

// API field names (from the Error schema) mapped to form fields.
const apiFields: Record<string, FieldName> = {
  user_id: "userId",
  amount: "amount",
  category: "category",
  spent_at: "spentAt",
  comment: "comment",
};

type Props = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** The expense to edit; undefined creates a new one. */
  expense?: Expense;
  /** Preselected member for a new expense (the one the panel is filtered by). */
  defaultUserId?: number;
  users: User[];
  categories: CategoryInfo[];
  /** Offered in edit mode: on phones the form is the only place to delete from. */
  onDelete?: (e: Expense) => void;
};

export function ExpenseFormDialog(props: Props) {
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent className="sm:max-w-md">
        {/* Remounted on every open, so the form starts from the current expense. */}
        {props.open && <ExpenseForm {...props} />}
      </DialogContent>
    </Dialog>
  );
}

function ExpenseForm({
  onOpenChange,
  expense,
  defaultUserId,
  users: allUsers,
  categories,
  onDelete,
}: Props) {
  // New expenses go to current members; editing keeps the owner even if removed.
  const users = allUsers.filter((u) => !u.removed_at || u.telegram_id === expense?.user_id);
  const today = toISODate(new Date());
  const [fields, setFields] = useState<Fields>(() =>
    expense
      ? {
          userId: String(expense.user_id),
          amount: String(expense.amount),
          category: expense.category,
          spentAt: expense.spent_at,
          comment: expense.comment ?? "",
        }
      : {
          userId: defaultUserId
            ? String(defaultUserId)
            : users.length === 1
              ? String(users[0].telegram_id)
              : "",
          amount: "",
          category: "",
          spentAt: today,
          comment: "",
        },
  );
  const [errors, setErrors] = useState<Errors>({});
  const create = useCreateExpense();
  const update = useUpdateExpense();
  const pending = create.isPending || update.isPending;

  const set = (name: FieldName) => (value: string) => {
    setFields((f) => ({ ...f, [name]: value }));
    setErrors((e) => ({ ...e, [name]: undefined }));
  };

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const { errors: found, amount } = validate(fields, today);
    setErrors(found);
    if (Object.keys(found).length > 0) return;

    const body = {
      user_id: Number(fields.userId),
      amount,
      category: fields.category as Category,
      spent_at: fields.spentAt,
      comment: fields.comment.trim(),
    };
    try {
      if (expense) {
        await update.mutateAsync({ id: expense.id, body });
        toast.success("Трата изменена");
      } else {
        await create.mutateAsync(body);
        toast.success(`Добавлено: ${formatMoney(amount)}`);
      }
      onOpenChange(false);
    } catch (err) {
      // Show server-side validation next to the field it concerns.
      if (err instanceof ApiError && err.field && apiFields[err.field]) {
        setErrors({ [apiFields[err.field]]: err.message });
      } else if (err instanceof ApiError && err.body?.error.code === "unknown_user") {
        setErrors({ userId: "Такого члена семьи нет" });
      } else {
        toast.error(err instanceof Error ? err.message : "Не удалось сохранить трату");
      }
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-6">
      <DialogHeader>
        <DialogTitle>{expense ? "Изменить трату" : "Новая трата"}</DialogTitle>
        <DialogDescription>
          {expense
            ? "Поменяйте нужные поля и сохраните."
            : "Трата появится у выбранного члена семьи."}
        </DialogDescription>
      </DialogHeader>

      <div className="flex flex-col gap-4">
        <FormField label="Член семьи" id="expense-user" error={errors.userId}>
          <Select value={fields.userId} onValueChange={set("userId")}>
            <SelectTrigger id="expense-user" className="h-10 w-full" aria-invalid={!!errors.userId}>
              <SelectValue placeholder="Выберите" />
            </SelectTrigger>
            <SelectContent>
              {users.map((u) => (
                <SelectItem key={u.telegram_id} value={String(u.telegram_id)}>
                  {memberName(u)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </FormField>

        <div className="grid grid-cols-2 gap-4">
          <FormField label="Сумма, ₽" id="expense-amount" error={errors.amount}>
            <Input
              id="expense-amount"
              inputMode="decimal"
              autoComplete="off"
              placeholder="350"
              className="h-10"
              value={fields.amount}
              onChange={(e) => set("amount")(e.target.value)}
              aria-invalid={!!errors.amount}
            />
          </FormField>
          <FormField label="Дата" id="expense-date" error={errors.spentAt}>
            <Input
              id="expense-date"
              type="date"
              max={today}
              className="h-10"
              value={fields.spentAt}
              onChange={(e) => set("spentAt")(e.target.value)}
              aria-invalid={!!errors.spentAt}
            />
          </FormField>
        </div>

        <FormField label="Категория" id="expense-category" error={errors.category}>
          <Select value={fields.category} onValueChange={set("category")}>
            <SelectTrigger
              id="expense-category"
              className="h-10 w-full"
              aria-invalid={!!errors.category}
            >
              <SelectValue placeholder="Выберите" />
            </SelectTrigger>
            <SelectContent>
              {categories.map((c) => (
                <SelectItem key={c.code} value={c.code}>
                  {capitalize(c.label)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </FormField>

        <FormField label="Комментарий" id="expense-comment" error={errors.comment}>
          <Input
            id="expense-comment"
            placeholder="кофе"
            maxLength={500}
            className="h-10"
            value={fields.comment}
            onChange={(e) => set("comment")(e.target.value)}
            aria-invalid={!!errors.comment}
          />
        </FormField>
      </div>

      <DialogFooter>
        {expense && onDelete && (
          <Button
            type="button"
            variant="ghost"
            className="h-10 text-destructive hover:text-destructive sm:mr-auto"
            onClick={() => {
              onOpenChange(false);
              onDelete(expense);
            }}
          >
            Удалить
          </Button>
        )}
        <Button
          type="button"
          variant="outline"
          className="h-10"
          onClick={() => onOpenChange(false)}
        >
          Отмена
        </Button>
        <Button type="submit" className="h-10" disabled={pending}>
          {pending ? "Сохранение…" : expense ? "Сохранить" : "Добавить"}
        </Button>
      </DialogFooter>
    </form>
  );
}

/** Client-side checks mirroring the server rules, for instant feedback. */
function validate(f: Fields, today: string): { errors: Errors; amount: number } {
  const errors: Errors = {};
  const normalized = f.amount.replace(/\s/g, "").replace(",", ".");
  const amount = Number(normalized);
  if (!f.userId) errors.userId = "Выберите члена семьи";
  if (!normalized) {
    errors.amount = "Укажите сумму";
  } else if (!/^\d+(\.\d{1,2})?$/.test(normalized) || amount <= 0) {
    errors.amount = "Положительное число, до 2 знаков после запятой";
  }
  if (!f.category) errors.category = "Выберите категорию";
  if (!f.spentAt) errors.spentAt = "Укажите дату";
  else if (f.spentAt > today) errors.spentAt = "Дата не может быть в будущем";
  return { errors, amount };
}

function FormField({
  label,
  id,
  error,
  children,
}: {
  label: string;
  id: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {error && (
        <p className="text-sm text-destructive" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}
