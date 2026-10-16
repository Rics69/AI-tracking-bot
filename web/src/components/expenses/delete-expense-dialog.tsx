"use client";

import { toast } from "sonner";

import type { Expense } from "@/api/client";
import { useDeleteExpense } from "@/api/queries";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { formatDate, formatMoney } from "@/lib/format";

type Props = {
  expense?: Expense;
  onOpenChange: (open: boolean) => void;
  memberLabel: (userId: number) => string;
};

export function DeleteExpenseDialog({ expense, onOpenChange, memberLabel }: Props) {
  const remove = useDeleteExpense();

  async function confirm() {
    if (!expense) return;
    try {
      await remove.mutateAsync(expense.id);
      toast.success("Трата удалена");
      onOpenChange(false);
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Не удалось удалить трату");
    }
  }

  return (
    <AlertDialog open={!!expense} onOpenChange={onOpenChange}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Удалить трату?</AlertDialogTitle>
          <AlertDialogDescription>
            {expense &&
              `${expense.comment ?? "Без комментария"} — ${formatMoney(expense.amount)}, ` +
                `${formatDate(expense.spent_at)}, ${memberLabel(expense.user_id)}. Это действие нельзя отменить.`}
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel className="h-10">Отмена</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            className="h-10"
            disabled={remove.isPending}
            // Keep the dialog open until the request finishes.
            onClick={(e) => {
              e.preventDefault();
              void confirm();
            }}
          >
            {remove.isPending ? "Удаление…" : "Удалить"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
