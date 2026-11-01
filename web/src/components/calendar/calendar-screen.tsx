"use client";

import { CalendarDaysIcon, CheckIcon, ChevronLeftIcon, ChevronRightIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useRouter } from "next/navigation";
import { useMemo, useState, type FormEvent } from "react";
import { toast } from "sonner";

import type { RegularPayment, Task, TaskRepeat } from "@/api/client";
import { useBudget, useCreateTask, useDeleteTask, useDoneTask, useRegularPayments, useTasks } from "@/api/queries";
import { PageHeader, Panel, Pill, PillButton, Segmented } from "@/components/kit";
import { confirm } from "@/components/operations/operation-sheet";
import { Input } from "@/components/ui/input";
import { Sheet, useTelegramBack } from "@/components/ui/sheet";
import { formatMoney, toISODate } from "@/lib/format";
import { haptic } from "@/lib/telegram";
import { cn } from "@/lib/utils";

type Mode = "week" | "month";

const weekdays = ["пн", "вт", "ср", "чт", "пт", "сб", "вс"];
const months = ["январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"];
const repeats: { value: TaskRepeat; label: string }[] = [
  { value: "none", label: "Не повторять" },
  { value: "daily", label: "Каждый день" },
  { value: "weekdays", label: "По будням" },
  { value: "weekly", label: "Каждую неделю" },
  { value: "monthly", label: "Каждый месяц" },
];

function monday(d: Date) {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate() - ((d.getDay() + 6) % 7));
}

/** The days a view shows: seven, or the whole weeks of a month. */
function daysOf(anchor: Date, mode: Mode): Date[] {
  const start = mode === "week" ? monday(anchor) : monday(new Date(anchor.getFullYear(), anchor.getMonth(), 1));
  const end =
    mode === "week"
      ? new Date(start.getFullYear(), start.getMonth(), start.getDate() + 6)
      : new Date(anchor.getFullYear(), anchor.getMonth() + 1, 0);
  const out: Date[] = [];
  for (let d = start; d <= end || out.length % 7 !== 0; d = new Date(d.getFullYear(), d.getMonth(), d.getDate() + 1)) {
    out.push(d);
  }
  return out;
}

/** The day a monthly date falls on: 31 means the last day of a short month. */
function clampDay(day: number, d: Date) {
  return Math.min(day, new Date(d.getFullYear(), d.getMonth() + 1, 0).getDate());
}

export function CalendarScreen() {
  const router = useRouter();
  useTelegramBack(true, () => router.back());
  const [now] = useState(() => new Date());
  const [mode, setMode] = useState<Mode>("month");
  const [anchor, setAnchor] = useState(now);
  const [selected, setSelected] = useState(() => toISODate(now));
  const [adding, setAdding] = useState(false);
  const today = toISODate(now);

  const days = useMemo(() => daysOf(anchor, mode), [anchor, mode]);
  const from = toISODate(days[0]);
  const to = toISODate(days[days.length - 1]);
  const tasks = useTasks(from, to).data?.items ?? [];
  const payments = useRegularPayments().data ?? [];
  const paydays = useBudget().data?.plan?.paydays ?? [];

  const paymentsOn = (d: Date) => payments.filter((p) => clampDay(p.day, d) === d.getDate());
  const isPayday = (d: Date) => paydays.some((p) => clampDay(p, d) === d.getDate());

  const shift = (step: number) => {
    haptic.select();
    setAnchor((a) =>
      mode === "week"
        ? new Date(a.getFullYear(), a.getMonth(), a.getDate() + step * 7)
        : new Date(a.getFullYear(), a.getMonth() + step, 1),
    );
  };

  const day = days.find((d) => toISODate(d) === selected);
  const dayTasks = tasks.filter((t) => t.date === selected);
  const dayPayments = day ? paymentsOn(day) : [];

  return (
    <>
      <PageHeader
        title="Календарь"
        action={
          <button
            type="button"
            aria-label="Новая задача"
            onClick={() => setAdding(true)}
            className="flex size-11 items-center justify-center rounded-full bg-primary text-primary-foreground"
          >
            <PlusIcon className="size-5" />
          </button>
        }
      />
      <Segmented
        label="Вид"
        value={mode}
        onChange={setMode}
        options={[
          { value: "month", label: "Месяц" },
          { value: "week", label: "Неделя" },
        ]}
      />
      <Panel
        icon={CalendarDaysIcon}
        title={`${months[anchor.getMonth()]} ${anchor.getFullYear()}`}
        action={
          <span className="flex gap-1">
            <button type="button" aria-label="Назад" onClick={() => shift(-1)} className="flex size-10 items-center justify-center rounded-full bg-muted">
              <ChevronLeftIcon className="size-5" />
            </button>
            <button type="button" aria-label="Вперёд" onClick={() => shift(1)} className="flex size-10 items-center justify-center rounded-full bg-muted">
              <ChevronRightIcon className="size-5" />
            </button>
          </span>
        }
      >
        <div className="grid grid-cols-7 gap-1 text-center">
          {weekdays.map((w) => (
            <span key={w} className="text-[12px] text-muted-foreground">
              {w}
            </span>
          ))}
          {days.map((d) => {
            const iso = toISODate(d);
            const open = tasks.some((t) => t.date === iso && !t.done);
            return (
              <button
                key={iso}
                type="button"
                aria-pressed={iso === selected}
                aria-label={iso}
                onClick={() => {
                  haptic.select();
                  setSelected(iso);
                }}
                className={cn(
                  "flex h-12 flex-col items-center justify-center gap-1 rounded-xl text-[15px] tabular-nums",
                  iso === selected ? "bg-primary text-primary-foreground" : "bg-muted/40",
                  mode === "month" && d.getMonth() !== anchor.getMonth() && "opacity-40",
                  iso === today && iso !== selected && "ring-1 ring-primary",
                )}
              >
                {d.getDate()}
                <span className="flex h-1.5 gap-0.5" aria-hidden>
                  {open && <span className="size-1.5 rounded-full bg-foreground" />}
                  {paymentsOn(d).length > 0 && <span className="size-1.5 rounded-full bg-destructive" />}
                  {isPayday(d) && <span className="size-1.5 rounded-full bg-gold" />}
                </span>
              </button>
            );
          })}
        </div>
        <div className="flex flex-wrap gap-x-4 gap-y-1 text-[12px] text-muted-foreground">
          <Legend className="bg-foreground" label="задачи" />
          <Legend className="bg-destructive" label="платежи" />
          <Legend className="bg-gold" label="зарплата" />
        </div>
      </Panel>

      <Panel icon={CheckIcon} title="На этот день">
        {day && isPayday(day) && <Pill tone="success">день зарплаты</Pill>}
        <DayPayments items={dayPayments} />
        {dayTasks.length === 0 && dayPayments.length === 0 ? (
          <p className="text-[15px] text-muted-foreground">Ничего не запланировано.</p>
        ) : (
          <ul className="flex flex-col gap-3">
            {dayTasks.map((t) => (
              <TaskRow key={`${t.id}-${t.date}`} task={t} />
            ))}
          </ul>
        )}
      </Panel>

      <Sheet open={adding} onOpenChange={setAdding} title="Новая задача" description="Что, когда и напомнить ли">
        {adding && <TaskForm date={selected} onDone={() => setAdding(false)} />}
      </Sheet>
    </>
  );
}

function Legend({ className, label }: { className: string; label: string }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className={cn("size-1.5 rounded-full", className)} aria-hidden />
      {label}
    </span>
  );
}

function DayPayments({ items }: { items: RegularPayment[] }) {
  if (items.length === 0) return null;
  return (
    <ul className="flex flex-col gap-2">
      {items.map((p) => (
        <li key={p.id} className="flex items-center justify-between gap-3 text-[15px]">
          <span className="truncate">{p.title}</span>
          <span className="shrink-0 font-medium tabular-nums">{formatMoney(p.amount)}</span>
        </li>
      ))}
    </ul>
  );
}

function TaskRow({ task: t }: { task: Task }) {
  const done = useDoneTask();
  const remove = useDeleteTask();
  async function onDelete() {
    if (!(await confirm(`Удалить задачу «${t.title}»?`))) return;
    remove.mutate(t.id, { onError: (err) => toast.error(err.message) });
  }
  return (
    <li className="flex items-center gap-3">
      <button
        type="button"
        role="checkbox"
        aria-checked={t.done}
        aria-label={`Сделано: ${t.title}`}
        disabled={t.done || done.isPending}
        onClick={() =>
          done.mutate(t.id, {
            onSuccess: () => {
              haptic.success();
              if (t.goal_id) toast.success("Отмечено. Не забудьте записать пополнение цели");
            },
            onError: (err) => toast.error(err.message),
          })
        }
        className={cn(
          "flex size-8 shrink-0 items-center justify-center rounded-full border border-input",
          t.done && "border-transparent bg-success-soft text-success",
        )}
      >
        {t.done && <CheckIcon className="size-4" />}
      </button>
      <span className={cn("flex min-w-0 flex-1 flex-col", t.done && "opacity-60")}>
        <span className="text-[15px] font-medium">{t.title}</span>
        {(t.time || t.repeat !== "none") && (
          <span className="text-[13px] text-muted-foreground">
            {[t.time, repeats.find((r) => r.value === t.repeat && r.value !== "none")?.label].filter(Boolean).join(" · ")}
          </span>
        )}
      </span>
      <button
        type="button"
        aria-label={`Удалить: ${t.title}`}
        onClick={() => void onDelete()}
        className="flex size-10 shrink-0 items-center justify-center rounded-full text-muted-foreground"
      >
        <Trash2Icon className="size-4" />
      </button>
    </li>
  );
}

function TaskForm({ date: initial, onDone }: { date: string; onDone: () => void }) {
  const create = useCreateTask();
  const [title, setTitle] = useState("");
  const [date, setDate] = useState(initial);
  const [time, setTime] = useState("");
  const [repeat, setRepeat] = useState<TaskRepeat>("none");

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    try {
      await create.mutateAsync({ title: title.trim(), date, repeat, ...(time && { time }) });
      haptic.success();
      onDone();
    } catch (err) {
      haptic.error();
      toast.error(err instanceof Error ? err.message : "Не удалось сохранить");
    }
  }

  const fieldClass = "h-11 min-w-0 rounded-full bg-muted px-4 text-[15px] outline-none";
  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4 pb-2">
      <div className="flex flex-col gap-2">
        <label htmlFor="task-title" className="text-sm font-medium text-muted-foreground">
          Что сделать
        </label>
        <Input id="task-title" maxLength={200} placeholder="Оплатить интернет" value={title} onChange={(e) => setTitle(e.target.value)} className="h-11 rounded-full px-4 text-[15px]" />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="flex min-w-0 flex-col gap-2">
          <label htmlFor="task-date" className="text-sm font-medium text-muted-foreground">
            Дата
          </label>
          <input id="task-date" type="date" value={date} onChange={(e) => setDate(e.target.value)} className={fieldClass} />
        </div>
        <div className="flex min-w-0 flex-col gap-2">
          <label htmlFor="task-time" className="text-sm font-medium text-muted-foreground">
            Напомнить в
          </label>
          <input id="task-time" type="time" value={time} onChange={(e) => setTime(e.target.value)} className={fieldClass} />
        </div>
      </div>
      <div className="flex flex-col gap-2">
        <label htmlFor="task-repeat" className="text-sm font-medium text-muted-foreground">
          Повтор
        </label>
        <select id="task-repeat" value={repeat} onChange={(e) => setRepeat(e.target.value as TaskRepeat)} className={fieldClass}>
          {repeats.map((r) => (
            <option key={r.value} value={r.value}>
              {r.label}
            </option>
          ))}
        </select>
      </div>
      <PillButton type="submit" className="h-12 w-full" disabled={create.isPending || !title.trim() || !date}>
        Запланировать
      </PillButton>
    </form>
  );
}
