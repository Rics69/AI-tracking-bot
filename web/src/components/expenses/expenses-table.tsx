"use client";

import { ChevronLeftIcon, ChevronRightIcon, PencilIcon, Trash2Icon } from "lucide-react";

import type { Expense } from "@/api/client";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatDate, formatMoney } from "@/lib/format";
import { cn } from "@/lib/utils";

import { capitalize } from "./filters-bar";

type Props = {
  items?: Expense[];
  total: number;
  page: number;
  pageSize: number;
  onPageChange: (page: number) => void;
  memberLabel: (userId: number) => string;
  categoryLabel: (code: string) => string;
  /** True while a new page or filter is loading over the previous data. */
  stale: boolean;
  onEdit: (e: Expense) => void;
  onDelete: (e: Expense) => void;
  /** Whether the user may change the expense: everyone changes only their own. */
  canEdit: (e: Expense) => boolean;
};

export function ExpensesTable({
  items,
  total,
  page,
  pageSize,
  onPageChange,
  memberLabel,
  categoryLabel,
  stale,
  onEdit,
  onDelete,
  canEdit,
}: Props) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const first = (page - 1) * pageSize + 1;
  const last = Math.min(page * pageSize, total);

  return (
    <div className="flex flex-col gap-4">
      {/* Phones: a list, so the amount stays visible without horizontal scrolling. */}
      <ul
        className={cn(
          "flex flex-col divide-y rounded-xl bg-card ring-1 ring-foreground/10 transition-opacity sm:hidden",
          stale && "opacity-60",
          items?.length === 0 && "hidden",
        )}
      >
        {!items
          ? Array.from({ length: 5 }, (_, i) => (
              <li key={i} className="px-4 py-3">
                <Skeleton className="h-10 w-full" />
              </li>
            ))
          : items.map((e) => (
              <li key={e.id}>
                <button
                  type="button"
                  disabled={!canEdit(e)}
                  onClick={() => onEdit(e)}
                  className="flex w-full disabled:cursor-default disabled:hover:bg-transparent items-center justify-between gap-4 px-4 py-3 text-left outline-none hover:bg-muted/50 focus-visible:bg-muted/50"
                >
                  <div className="flex min-w-0 flex-col gap-1">
                    <span className="truncate font-medium">
                      {e.comment ?? capitalize(categoryLabel(e.category))}
                    </span>
                    <span className="truncate text-sm text-muted-foreground">
                      {formatDate(e.date)} · {memberLabel(e.user_id)} · {categoryLabel(e.category)}
                    </span>
                  </div>
                  <span className="shrink-0 font-medium tabular-nums">{formatMoney(e.amount)}</span>
                </button>
              </li>
            ))}
      </ul>

      <div
        className={cn(
          "hidden overflow-x-auto rounded-xl bg-card ring-1 ring-foreground/10 sm:block",
          items?.length === 0 && "sm:hidden",
        )}
      >
        <Table className={cn("min-w-[720px] transition-opacity", stale && "opacity-60")}>
          <TableHeader>
            <TableRow>
              <TableHead className="w-28 pl-4">Дата</TableHead>
              <TableHead>Член семьи</TableHead>
              <TableHead>Категория</TableHead>
              <TableHead>Комментарий</TableHead>
              <TableHead className="text-right">Сумма</TableHead>
              <TableHead className="w-24 pr-4">
                <span className="sr-only">Действия</span>
              </TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {!items
              ? Array.from({ length: 5 }, (_, i) => (
                  <TableRow key={i}>
                    {Array.from({ length: 6 }, (_, j) => (
                      <TableCell key={j} className="py-3 first:pl-4 last:pr-4">
                        <Skeleton className="h-5 w-full" />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              : items.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className="py-3 pl-4 whitespace-nowrap text-muted-foreground">
                      {formatDate(e.date)}
                    </TableCell>
                    <TableCell className="py-3">{memberLabel(e.user_id)}</TableCell>
                    <TableCell className="py-3">
                      <Badge variant="secondary">{capitalize(categoryLabel(e.category))}</Badge>
                    </TableCell>
                    <TableCell className="max-w-64 truncate py-3">
                      {e.comment ?? <span className="text-muted-foreground">—</span>}
                    </TableCell>
                    <TableCell className="py-3 text-right font-medium tabular-nums">
                      {formatMoney(e.amount)}
                    </TableCell>
                    <TableCell className="py-1 pr-4 text-right whitespace-nowrap">
                      {canEdit(e) && (
                        <>
                          <Button
                            variant="ghost"
                            size="icon-lg"
                            aria-label="Изменить трату"
                            onClick={() => onEdit(e)}
                          >
                            <PencilIcon />
                          </Button>
                          <Button
                            variant="ghost"
                            size="icon-lg"
                            aria-label="Удалить трату"
                            className="text-muted-foreground hover:text-destructive"
                            onClick={() => onDelete(e)}
                          >
                            <Trash2Icon />
                          </Button>
                        </>
                      )}
                    </TableCell>
                  </TableRow>
                ))}
          </TableBody>
        </Table>
      </div>

      {items?.length === 0 && (
        <div className="flex flex-col items-center gap-2 rounded-xl bg-card px-4 py-12 text-center ring-1 ring-foreground/10">
          <p className="font-medium">Трат не найдено</p>
          <p className="text-sm text-muted-foreground">
            Измените фильтры, добавьте трату кнопкой выше или через Telegram-бота: «кофе 350».
          </p>
        </div>
      )}

      {total > pageSize && (
        <div className="flex items-center justify-between gap-4 text-sm text-muted-foreground">
          <span className="tabular-nums">
            {first}–{last} из {total}
          </span>
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="icon-lg"
              aria-label="Предыдущая страница"
              disabled={page <= 1}
              onClick={() => onPageChange(page - 1)}
            >
              <ChevronLeftIcon />
            </Button>
            <Button
              variant="outline"
              size="icon-lg"
              aria-label="Следующая страница"
              disabled={page >= pages}
              onClick={() => onPageChange(page + 1)}
            >
              <ChevronRightIcon />
            </Button>
          </div>
        </div>
      )}
    </div>
  );
}
