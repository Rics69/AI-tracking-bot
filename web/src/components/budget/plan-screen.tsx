"use client";

import { CalendarClockIcon, CheckIcon, PlusIcon, RepeatIcon, TargetIcon, Trash2Icon } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";

import { ApiError, type Category, type Goal, type Plan, type RegularPayment } from "@/api/client";
import {
  useBudget,
  useCreateGoal,
  useCreatePayment,
  useDeletePayment,
  useDeletePlan,
  useGoals,
  usePayPayment,
  useRegularPayments,
  useSetPlan,
} from "@/api/queries";
import { DepositSheet, GoalRow } from "@/components/budget/goals";
import { Card, PageHeader, Panel, Pill, PillButton } from "@/components/kit";
import { confirm } from "@/components/operations/operation-sheet";
import { Input } from "@/components/ui/input";
import { useTelegramBack } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { texts } from "@/content/texts";
import { amountInput, parseAmount } from "@/lib/amount";
import { capitalize, categoryIcons, useCategoryInfo } from "@/lib/categories";
import { formatDate, formatMoney, toISODate } from "@/lib/format";
import { haptic } from "@/lib/telegram";
import { cn } from "@/lib/utils";

type Step = 0 | 1 | 2 | "done";

/**
 * "Цели и лимит": the first visit with nothing set up is a wizard of three
 * skippable steps (payday, regular payments, a goal); afterwards an overview
 * where each part is edited in place.
 */
export function PlanScreen() {
  const router = useRouter();
  useTelegramBack(true, () => router.back());
  const budget = useBudget();
  const goals = useGoals();
  const payments = useRegularPayments();
  const loaded = !!(budget.data && goals.data && payments.data);
  const empty = loaded && !budget.data?.plan && goals.data?.length === 0 && payments.data?.length === 0;

  // Decided once, when the data arrives: saving a step must not end the wizard.
  const [step, setStepState] = useState<Step>();
  if (step === undefined && loaded) setStepState(empty ? 0 : "done");
  const setStep = (s: Step) => {
    setStepState(s);
    window.scrollTo({ top: 0 });
  };

  if (step === undefined) return <Skeleton className="h-72 w-full rounded-[22px]" />;
  if (step === "done") {
    return (
      <>
        <PageHeader section="Цели и лимит" title="Цели и лимит" />
        <PlanPanel plan={budget.data?.plan} />
        <PaymentsPanel payments={payments.data ?? []} />
        <GoalsSection goals={goals.data ?? []} />
      </>
    );
  }

  const next = () => setStep(step === 2 ? "done" : ((step + 1) as Step));
  const finish = () => {
    toast.success(texts.planDone);
    setStep("done");
  };
  const { title, hint } = texts.planSteps[step];
  return (
    <>
      <PageHeader section="Цели и лимит" title={title} />
      <Stepper step={step} />
      <p className="px-1 text-[15px] leading-snug text-muted-foreground">{hint}</p>
      <Card className="flex flex-col gap-5 p-4">
        {step === 0 && <PaydayForm plan={budget.data?.plan} submit="Дальше" onSaved={next} />}
        {step === 1 && <PaymentsEditor payments={payments.data ?? []} startOpen />}
        {step === 2 && <GoalForm submit="Готово" onSaved={finish} />}
      </Card>
      <div className="flex gap-2">
        {step === 1 ? (
          <PillButton className="h-12 flex-1" onClick={next}>
            Дальше
          </PillButton>
        ) : (
          <PillButton variant="outline" className="h-12 flex-1" onClick={step === 2 ? finish : next}>
            Пропустить
          </PillButton>
        )}
      </div>
      <p className="px-1 text-center text-[13px] text-muted-foreground">{texts.planIntro}</p>
    </>
  );
}

function Stepper({ step }: { step: number }) {
  return (
    <div className="flex flex-col gap-2 px-1">
      <div className="grid grid-cols-3 gap-1.5" aria-hidden>
        {[0, 1, 2].map((i) => (
          <span key={i} className={cn("h-1.5 rounded-full", i <= step ? "bg-foreground" : "bg-muted-foreground/25")} />
        ))}
      </div>
      <span className="text-[13px] text-muted-foreground">Шаг {step + 1} из 3</span>
    </div>
  );
}

// Payday and the limit.

function PlanPanel({ plan }: { plan?: Plan }) {
  const [editing, setEditing] = useState(false);
  const remove = useDeletePlan();

  async function turnOff() {
    if (!(await confirm("Выключить дневной лимит? Цели и регулярные платежи останутся."))) return;
    try {
      await remove.mutateAsync();
      haptic.success();
      toast.success("Лимит выключен");
    } catch (err) {
      haptic.error();
      toast.error(err instanceof Error ? err.message : "Не удалось выключить");
    }
  }

  return (
    <Panel icon={CalendarClockIcon} title="Зарплата и лимит">
      {!plan || editing ? (
        <>
          {!plan && <p className="text-[14px] text-muted-foreground">{texts.planSteps[0].hint}</p>}
          <PaydayForm plan={plan} submit={plan ? "Сохранить" : "Включить лимит"} onSaved={() => setEditing(false)} />
          {editing && (
            <PillButton variant="outline" className="h-11 w-full" onClick={() => setEditing(false)}>
              Отмена
            </PillButton>
          )}
        </>
      ) : (
        <>
          <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-[15px]">
            <dt className="text-muted-foreground">Зарплата</dt>
            <dd className="text-right">{plan.paydays.join(" и ")} числа</dd>
            <dt className="text-muted-foreground">Доход за период</dt>
            <dd className="text-right">{plan.expected_income ? formatMoney(plan.expected_income) : "по записанным"}</dd>
            <dt className="text-muted-foreground">На цели за период</dt>
            <dd className="text-right">{plan.goal_contribution ? formatMoney(plan.goal_contribution) : "—"}</dd>
          </dl>
          <Reminders plan={plan} />
          <div className="flex gap-2">
            <PillButton variant="outline" className="h-11 flex-1" onClick={() => setEditing(true)}>
              Изменить
            </PillButton>
            <PillButton
              variant="outline"
              className="h-11 flex-1 text-destructive"
              disabled={remove.isPending}
              onClick={() => void turnOff()}
            >
              Выключить
            </PillButton>
          </div>
        </>
      )}
    </Panel>
  );
}

const DAYS = Array.from({ length: 31 }, (_, i) => i + 1);

function PaydayForm({ plan, submit, onSaved }: { plan?: Plan; submit: string; onSaved: () => void }) {
  const [days, setDays] = useState<number[]>(plan?.paydays ?? []);
  const [income, setIncome] = useState(plan?.expected_income ? String(plan.expected_income) : "");
  const [contribution, setContribution] = useState(plan?.goal_contribution ? String(plan.goal_contribution) : "");
  const [errors, setErrors] = useState<Partial<Record<"paydays" | "expected_income" | "goal_contribution", string>>>({});
  const save = useSetPlan();

  const toggle = (d: number) => {
    haptic.select();
    setErrors((e) => ({ ...e, paydays: undefined }));
    // Two paydays at most: a third choice replaces the earlier one.
    setDays((prev) => (prev.includes(d) ? prev.filter((x) => x !== d) : [...prev, d].slice(-2)));
  };

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const found: typeof errors = {};
    if (days.length === 0) found.paydays = "Выберите день зарплаты";
    const optional = (v: string, field: keyof typeof errors) => {
      if (!v.trim()) return undefined;
      const parsed = parseAmount(v);
      if (typeof parsed === "number") return parsed;
      found[field] = parsed.error;
    };
    const expected = optional(income, "expected_income");
    const goal = optional(contribution, "goal_contribution");
    setErrors(found);
    if (Object.keys(found).length > 0) {
      haptic.error();
      return;
    }
    try {
      await save.mutateAsync({
        paydays: [...days].sort((a, b) => a - b),
        expected_income: expected,
        goal_contribution: goal,
        reminder_hour: plan?.reminder_hour ?? 9,
        reminders: plan?.reminders ?? true,
      });
      haptic.success();
      onSaved();
    } catch (err) {
      haptic.error();
      if (err instanceof ApiError && err.field && err.field in found) setErrors({ [err.field]: err.message });
      else toast.error(err instanceof Error ? err.message : "Не удалось сохранить");
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-5">
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium text-muted-foreground">
          День зарплаты {days.length === 2 ? "(два раза в месяц)" : "— один или два"}
        </legend>
        <div className="grid grid-cols-7 gap-1.5">
          {DAYS.map((d) => (
            <button
              key={d}
              type="button"
              aria-pressed={days.includes(d)}
              onClick={() => toggle(d)}
              className={cn(
                "flex aspect-square items-center justify-center rounded-full text-[14px] tabular-nums transition-colors",
                days.includes(d) ? "bg-primary font-medium text-primary-foreground" : "bg-muted text-foreground",
              )}
            >
              {d}
            </button>
          ))}
        </div>
        <FieldError text={errors.paydays} />
      </fieldset>
      <MoneyInput
        id="plan-income"
        label={days.length === 2 ? "Доход за полмесяца — необязательно" : "Доход за месяц — необязательно"}
        hint="Без него лимит считается по записанным доходам."
        value={income}
        onChange={(v) => {
          setIncome(v);
          setErrors((e) => ({ ...e, expected_income: undefined }));
        }}
        error={errors.expected_income}
      />
      <MoneyInput
        id="plan-goal"
        label="Откладывать на цели за период — необязательно"
        value={contribution}
        onChange={(v) => {
          setContribution(v);
          setErrors((e) => ({ ...e, goal_contribution: undefined }));
        }}
        error={errors.goal_contribution}
      />
      <PillButton type="submit" className="h-12 w-full" disabled={save.isPending}>
        {save.isPending ? "Сохранение…" : submit}
      </PillButton>
    </form>
  );
}

const HOURS = Array.from({ length: 17 }, (_, i) => i + 6); // 6:00–22:00

function Reminders({ plan }: { plan: Plan }) {
  const save = useSetPlan();
  const update = (change: { reminders?: boolean; reminder_hour?: number }) =>
    save.mutate(
      {
        paydays: plan.paydays,
        expected_income: plan.expected_income,
        goal_contribution: plan.goal_contribution,
        reminders: plan.reminders,
        reminder_hour: plan.reminder_hour,
        ...change,
      },
      { onError: (err) => toast.error(err instanceof Error ? err.message : "Не удалось сохранить") },
    );
  return (
    <div className="flex flex-col gap-3 rounded-2xl bg-muted p-3">
      <label className="flex items-center justify-between gap-3">
        <span className="flex flex-col">
          <span className="text-[15px]">Напоминать о лимите</span>
          <span className="text-[13px] text-muted-foreground">Бот утром пришлёт лимит и платежи на завтра</span>
        </span>
        <Switch checked={plan.reminders} onCheckedChange={(on) => update({ reminders: on })} disabled={save.isPending} />
      </label>
      {plan.reminders && (
        <label className="flex items-center justify-between gap-3">
          <span className="text-[15px]">Во сколько</span>
          <select
            value={plan.reminder_hour}
            onChange={(e) => update({ reminder_hour: Number(e.target.value) })}
            className="h-10 rounded-full bg-card px-4 text-[15px] tabular-nums outline-none"
          >
            {HOURS.map((h) => (
              <option key={h} value={h}>
                {String(h).padStart(2, "0")}:00
              </option>
            ))}
          </select>
        </label>
      )}
    </div>
  );
}

// Regular payments.

function PaymentsPanel({ payments }: { payments: RegularPayment[] }) {
  return (
    <Panel icon={RepeatIcon} title="Регулярные платежи">
      <PaymentsEditor payments={payments} />
    </Panel>
  );
}

function PaymentsEditor({ payments, startOpen }: { payments: RegularPayment[]; startOpen?: boolean }) {
  const [adding, setAdding] = useState(!!startOpen && payments.length === 0);
  const [today] = useState(() => toISODate(new Date()));
  return (
    <div className="flex flex-col gap-4">
      {payments.length === 0 && !adding && <p className="text-[14px] text-muted-foreground">{texts.paymentsEmpty}</p>}
      {payments.length > 0 && (
        <ul className="flex flex-col divide-y divide-border">
          {payments.map((p) => (
            <li key={p.id} className="py-3 first:pt-0">
              <PaymentRow payment={p} today={today} />
            </li>
          ))}
        </ul>
      )}
      {adding ? (
        <PaymentForm onDone={() => setAdding(false)} />
      ) : (
        <PillButton variant="outline" icon={PlusIcon} className="h-11 w-full" onClick={() => setAdding(true)}>
          Добавить платёж
        </PillButton>
      )}
    </div>
  );
}

function PaymentRow({ payment: p, today }: { payment: RegularPayment; today: string }) {
  const pay = usePayPayment();
  const remove = useDeletePayment();
  const Icon = categoryIcons[p.category];
  const status: ReactNode =
    p.due_date < today ? (
      <Pill tone="danger">не оплачен с {formatDate(p.due_date)}</Pill>
    ) : p.due_date === today ? (
      <Pill tone="warning">сегодня</Pill>
    ) : (
      <Pill>{formatDate(p.due_date)}</Pill>
    );

  async function onPay() {
    try {
      await pay.mutateAsync(p.id);
      haptic.success();
      toast.success(`Записано: ${p.title} — ${formatMoney(p.amount)}`);
    } catch (err) {
      haptic.error();
      toast.error(err instanceof Error ? err.message : "Не удалось записать");
    }
  }

  async function onDelete() {
    if (!(await confirm(`Удалить платёж «${p.title}»? Записанные траты останутся.`))) return;
    try {
      await remove.mutateAsync(p.id);
      haptic.success();
    } catch (err) {
      haptic.error();
      toast.error(err instanceof Error ? err.message : "Не удалось удалить");
    }
  }

  return (
    <div className="flex gap-3">
      <span aria-hidden className="flex size-10 shrink-0 items-center justify-center rounded-full bg-muted">
        <Icon className="size-[18px]" strokeWidth={1.75} />
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-2">
        <div className="flex items-start justify-between gap-2">
          <span className="flex min-w-0 flex-col">
            <span className="truncate text-[15px] font-medium">{p.title}</span>
            <span className="text-[13px] text-muted-foreground tabular-nums">
              {formatMoney(p.amount)} · каждое {p.day}-е
            </span>
          </span>
          {status}
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => void onPay()}
            disabled={pay.isPending}
            className="inline-flex h-8 items-center gap-1 rounded-full bg-primary px-3 text-[13px] font-medium text-primary-foreground disabled:opacity-50"
          >
            <CheckIcon className="size-4" strokeWidth={2} />
            Оплачено
          </button>
          <button
            type="button"
            aria-label={`Удалить платёж «${p.title}»`}
            onClick={() => void onDelete()}
            className="flex size-8 items-center justify-center rounded-full text-muted-foreground"
          >
            <Trash2Icon className="size-[18px]" strokeWidth={1.75} />
          </button>
        </div>
      </div>
    </div>
  );
}

function PaymentForm({ onDone }: { onDone: () => void }) {
  const [title, setTitle] = useState("");
  const [amount, setAmount] = useState("");
  const [day, setDay] = useState(1);
  const [category, setCategory] = useState<Category>("housing");
  const [errors, setErrors] = useState<Partial<Record<"title" | "amount" | "day" | "category", string>>>({});
  const categories = useCategoryInfo();
  const create = useCreatePayment();

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const found: typeof errors = {};
    if (!title.trim()) found.title = "Как назвать платёж?";
    const parsed = parseAmount(amount);
    if (typeof parsed !== "number") found.amount = parsed.error;
    setErrors(found);
    if (Object.keys(found).length > 0 || typeof parsed !== "number") {
      haptic.error();
      return;
    }
    try {
      await create.mutateAsync({ title: title.trim(), amount: parsed, day, category });
      haptic.success();
      onDone();
    } catch (err) {
      haptic.error();
      if (err instanceof ApiError && err.field && ["title", "amount", "day", "category"].includes(err.field)) {
        setErrors({ [err.field]: err.message });
      } else toast.error(err instanceof Error ? err.message : "Не удалось сохранить");
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4 rounded-2xl bg-muted p-3">
      <TextInput
        id="payment-title"
        label="Название"
        placeholder="Аренда, интернет, подписка…"
        value={title}
        onChange={(v) => {
          setTitle(v);
          setErrors((e) => ({ ...e, title: undefined }));
        }}
        error={errors.title}
      />
      <div className="grid grid-cols-[1fr_auto] gap-3">
        <MoneyInput
          id="payment-amount"
          label="Сумма"
          value={amount}
          onChange={(v) => {
            setAmount(v);
            setErrors((e) => ({ ...e, amount: undefined }));
          }}
          error={errors.amount}
        />
        <label className="flex flex-col gap-2">
          <span className="text-sm font-medium text-muted-foreground">Число</span>
          <select
            value={day}
            onChange={(e) => setDay(Number(e.target.value))}
            className="h-11 rounded-full bg-card px-4 text-[15px] tabular-nums outline-none"
          >
            {DAYS.map((d) => (
              <option key={d} value={d}>
                {d}
              </option>
            ))}
          </select>
        </label>
      </div>
      <fieldset className="flex flex-col gap-2">
        <legend className="mb-2 text-sm font-medium text-muted-foreground">Категория</legend>
        <div className="flex flex-wrap gap-1.5">
          {categories.byKind.expense.map((c) => (
            <button
              key={c.code}
              type="button"
              aria-pressed={category === c.code}
              onClick={() => {
                haptic.select();
                setCategory(c.code);
              }}
              className={cn(
                "h-9 rounded-full px-3 text-[14px] transition-colors",
                category === c.code ? "bg-primary text-primary-foreground" : "bg-card text-foreground",
              )}
            >
              {capitalize(c.label)}
            </button>
          ))}
        </div>
        <FieldError text={errors.category} />
      </fieldset>
      <div className="flex gap-2">
        <PillButton variant="outline" className="h-11 flex-1" onClick={onDone}>
          Отмена
        </PillButton>
        <PillButton type="submit" className="h-11 flex-1" disabled={create.isPending}>
          Добавить
        </PillButton>
      </div>
    </form>
  );
}

// Goals.

function GoalsSection({ goals }: { goals: Goal[] }) {
  const [adding, setAdding] = useState(false);
  const [depositTo, setDepositTo] = useState<Goal | null>(null);
  return (
    <Panel icon={TargetIcon} title="Цели">
      {goals.length === 0 && !adding && <p className="text-[14px] text-muted-foreground">{texts.goalsEmpty}</p>}
      {goals.length > 0 && (
        <ul className="flex flex-col gap-4">
          {goals.map((g) => (
            <li key={g.id}>
              <GoalRow goal={g} onDeposit={() => setDepositTo(g)} deletable />
            </li>
          ))}
        </ul>
      )}
      {adding ? (
        <div className="rounded-2xl bg-muted p-3">
          <GoalForm submit="Добавить" onSaved={() => setAdding(false)} onCancel={() => setAdding(false)} />
        </div>
      ) : (
        <PillButton variant="outline" icon={PlusIcon} className="h-11 w-full" onClick={() => setAdding(true)}>
          Новая цель
        </PillButton>
      )}
      <DepositSheet goal={depositTo} onClose={() => setDepositTo(null)} />
    </Panel>
  );
}

function GoalForm({ submit, onSaved, onCancel }: { submit: string; onSaved: () => void; onCancel?: () => void }) {
  const [today] = useState(() => toISODate(new Date()));
  const [title, setTitle] = useState("");
  const [target, setTarget] = useState("");
  const [deadline, setDeadline] = useState("");
  const [errors, setErrors] = useState<Partial<Record<"title" | "target" | "deadline", string>>>({});
  const create = useCreateGoal();

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const found: typeof errors = {};
    if (!title.trim()) found.title = "На что копите?";
    const parsed = parseAmount(target);
    if (typeof parsed !== "number") found.target = parsed.error;
    if (deadline && deadline < today) found.deadline = "Срок не может быть в прошлом";
    setErrors(found);
    if (Object.keys(found).length > 0 || typeof parsed !== "number") {
      haptic.error();
      return;
    }
    try {
      await create.mutateAsync({ title: title.trim(), target: parsed, deadline: deadline || undefined });
      haptic.success();
      toast.success(`Цель «${title.trim()}» поставлена`);
      onSaved();
    } catch (err) {
      haptic.error();
      if (err instanceof ApiError && err.field && ["title", "target", "deadline"].includes(err.field)) {
        setErrors({ [err.field]: err.message });
      } else toast.error(err instanceof Error ? err.message : "Не удалось сохранить");
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4">
      <TextInput
        id="goal-title"
        label="На что копим"
        placeholder="Отпуск, ноутбук, подушка…"
        value={title}
        onChange={(v) => {
          setTitle(v);
          setErrors((e) => ({ ...e, title: undefined }));
        }}
        error={errors.title}
      />
      <MoneyInput
        id="goal-target"
        label="Сколько нужно"
        value={target}
        onChange={(v) => {
          setTarget(v);
          setErrors((e) => ({ ...e, target: undefined }));
        }}
        error={errors.target}
      />
      <label className="flex flex-col gap-2">
        <span className="text-sm font-medium text-muted-foreground">К какому сроку — необязательно</span>
        <input
          type="date"
          min={today}
          value={deadline}
          onChange={(e) => {
            setDeadline(e.target.value);
            setErrors((er) => ({ ...er, deadline: undefined }));
          }}
          className="h-11 rounded-full bg-card px-4 text-[15px] outline-none"
        />
        <FieldError text={errors.deadline} />
      </label>
      <div className="flex gap-2">
        {onCancel && (
          <PillButton variant="outline" className="h-11 flex-1" onClick={onCancel}>
            Отмена
          </PillButton>
        )}
        <PillButton type="submit" className="h-11 flex-1" disabled={create.isPending}>
          {create.isPending ? "Сохранение…" : submit}
        </PillButton>
      </div>
    </form>
  );
}

// Fields.

function TextInput({
  id,
  label,
  placeholder,
  value,
  onChange,
  error,
}: {
  id: string;
  label: string;
  placeholder?: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
}) {
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="text-sm font-medium text-muted-foreground">
        {label}
      </label>
      <Input
        id={id}
        placeholder={placeholder}
        maxLength={100}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        aria-invalid={!!error}
        className="h-11 rounded-full bg-card px-4 text-[15px]"
      />
      <FieldError text={error} />
    </div>
  );
}

function MoneyInput({
  id,
  label,
  hint,
  value,
  onChange,
  error,
}: {
  id: string;
  label: string;
  hint?: string;
  value: string;
  onChange: (v: string) => void;
  error?: string;
}) {
  return (
    <div className="flex flex-col gap-2">
      <label htmlFor={id} className="text-sm font-medium text-muted-foreground">
        {label}
      </label>
      <div className="relative">
        <Input
          id={id}
          inputMode="decimal"
          autoComplete="off"
          placeholder="0"
          value={value}
          onChange={(e) => onChange(amountInput(e.target.value))}
          aria-invalid={!!error}
          className="h-11 rounded-full bg-card pr-10 pl-4 text-[15px] tabular-nums"
        />
        <span className="pointer-events-none absolute top-1/2 right-4 -translate-y-1/2 text-muted-foreground">₽</span>
      </div>
      {hint && !error && <span className="text-[12px] text-muted-foreground">{hint}</span>}
      <FieldError text={error} />
    </div>
  );
}

function FieldError({ text }: { text?: string }) {
  if (!text) return null;
  return (
    <p role="alert" className="text-[13px] text-destructive">
      {text}
    </p>
  );
}
