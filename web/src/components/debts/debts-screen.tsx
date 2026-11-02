"use client";

import {
  BanknoteIcon,
  CreditCardIcon,
  HandCoinsIcon,
  LandmarkIcon,
  type LucideIcon,
  PlusIcon,
  ScaleIcon,
  ShoppingBagIcon,
  Trash2Icon,
  TrendingUpIcon,
  UserIcon,
} from "lucide-react";
import { useRouter } from "next/navigation";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { ApiError, type Debt, type DebtKind } from "@/api/client";
import {
  useCancelDebtPayment,
  useCancelLastPayment,
  useCreateDebt,
  useDebtPayments,
  useDebts,
  useDeleteDebt,
  useNetWorth,
  usePayDebt,
  useUpdateDebt,
} from "@/api/queries";
import { Field, fail, inputClass, selectClass } from "@/components/accounts/accounts-screen";
import { Card, IconCircle, PageHeader, Panel, PillButton } from "@/components/kit";
import { confirm } from "@/components/operations/operation-sheet";
import { Input } from "@/components/ui/input";
import { Sheet, useTelegramBack } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { amountInput, parseAmount } from "@/lib/amount";
import { formatDate, formatMoney } from "@/lib/format";
import { haptic } from "@/lib/telegram";

import { DebtPlan } from "./debt-plan";
import { NetWorthChart, signedMoney } from "./net-worth-chart";

const kinds: { value: DebtKind; label: string; icon: LucideIcon }[] = [
  { value: "card", label: "Кредитка", icon: CreditCardIcon },
  { value: "loan", label: "Кредит", icon: LandmarkIcon },
  { value: "installment", label: "Рассрочка", icon: ShoppingBagIcon },
  { value: "microloan", label: "Микрозайм", icon: BanknoteIcon },
  { value: "friend", label: "Знакомому", icon: UserIcon },
];

function kindOf(k: DebtKind) {
  return kinds.find((x) => x.value === k) ?? kinds[0];
}

function subtitle(d: Debt) {
  const parts = [kindOf(d.kind).label];
  if (d.rate > 0) parts.push(`${String(d.rate).replace(".", ",")}%`);
  if (d.due_day) parts.push(`${d.due_day} числа`);
  return parts.join(" · ");
}

/** A percent or a day of the month, typed by hand: an empty field is left out. */
function parseNumber(text: string): number | undefined {
  const value = Number(text.replace(/\s/g, "").replace(",", "."));
  return text.trim() && Number.isFinite(value) ? value : undefined;
}

export function DebtsScreen() {
  const router = useRouter();
  useTelegramBack(true, () => router.back());
  const debts = useDebts();
  const worth = useNetWorth();
  const [adding, setAdding] = useState(false);
  const [openId, setOpenId] = useState<number | null>(null);
  const items = debts.data?.items;
  const open = items?.find((d) => d.id === openId);
  return (
    <>
      <PageHeader title="Долги и капитал" />
      <Card className="flex flex-col gap-1 border-transparent bg-hero p-5 text-hero-foreground">
        <span className="text-[14px] opacity-80">Чистый капитал</span>
        {worth.data ? (
          <>
            <span className="text-[34px] font-semibold tracking-tight text-gold tabular-nums">{signedMoney(worth.data.net)}</span>
            <span className="text-[13px] opacity-70 tabular-nums">
              На счетах {signedMoney(worth.data.assets)} · долгов {formatMoney(worth.data.debts)}
            </span>
          </>
        ) : (
          <Skeleton className="h-10 w-40 bg-hero-foreground/15" />
        )}
      </Card>

      {worth.data && worth.data.history.length > 1 && (
        <Panel icon={TrendingUpIcon} title="Капитал по месяцам">
          <NetWorthChart points={worth.data.history} />
        </Panel>
      )}

      <PillButton icon={PlusIcon} className="w-full" onClick={() => setAdding(true)}>
        Долг
      </PillButton>

      <Panel icon={ScaleIcon} title="Мои долги">
        {!items ? (
          <Skeleton className="h-28 w-full rounded-2xl" />
        ) : items.length === 0 ? (
          <p className="text-[14px] text-muted-foreground">Долгов нет. Если появятся, запишите их здесь или напишите боту.</p>
        ) : (
          <ul className="flex flex-col">
            {items.map((d) => (
              <li key={d.id}>
                <button
                  type="button"
                  onClick={() => setOpenId(d.id)}
                  className="flex min-h-14 w-full items-center gap-3 rounded-2xl px-1 py-1.5 text-left active:bg-muted"
                >
                  <IconCircle icon={kindOf(d.kind).icon} className="size-10" />
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className="truncate text-[15px] font-medium">{d.name}</span>
                    <span className="truncate text-[13px] text-muted-foreground">{subtitle(d)}</span>
                  </span>
                  <span className="text-[15px] font-medium tabular-nums">{formatMoney(d.balance)}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      {items && items.length > 0 && <DebtPlan />}

      <Sheet open={adding} onOpenChange={setAdding} title="Новый долг" description="Сколько осталось отдать и на каких условиях">
        {adding && <DebtForm onDone={() => setAdding(false)} />}
      </Sheet>
      <Sheet open={open !== undefined} onOpenChange={(o) => !o && setOpenId(null)} title={open?.name ?? "Долг"} description={open && subtitle(open)}>
        {open && <DebtDetails key={open.id} debt={open} onDone={() => setOpenId(null)} />}
      </Sheet>
    </>
  );
}

function DebtForm({ onDone }: { onDone: () => void }) {
  const create = useCreateDebt();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<DebtKind>("card");
  const [balance, setBalance] = useState("");
  const [rate, setRate] = useState("");
  const [minimum, setMinimum] = useState("");
  const [dueDay, setDueDay] = useState("");
  const [error, setError] = useState<string>();

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const amount = parseAmount(balance);
    if (typeof amount !== "number") {
      setError(amount.error);
      return;
    }
    try {
      await create.mutateAsync({
        name: name.trim(),
        kind,
        balance: amount,
        rate: parseNumber(rate),
        min_payment: parseNumber(minimum),
        due_day: parseNumber(dueDay),
      });
      haptic.success();
      onDone();
    } catch (err) {
      if (err instanceof ApiError && err.field) setError(err.message);
      else fail(err);
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4 pb-2">
      <Field id="debt-name" label="Название">
        <Input id="debt-name" maxLength={60} placeholder="Кредитка" value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field id="debt-kind" label="Вид">
          <select id="debt-kind" value={kind} onChange={(e) => setKind(e.target.value as DebtKind)} className={selectClass}>
            {kinds.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
        </Field>
        <Field id="debt-balance" label="Осталось отдать">
          <Input id="debt-balance" inputMode="decimal" placeholder="0" value={balance} onChange={(e) => setBalance(amountInput(e.target.value))} className={inputClass} />
        </Field>
      </div>
      <div className="grid grid-cols-3 gap-3">
        <Field id="debt-rate" label="Ставка, %">
          <Input id="debt-rate" inputMode="decimal" placeholder="0" value={rate} onChange={(e) => setRate(amountInput(e.target.value))} className={inputClass} />
        </Field>
        <Field id="debt-min" label="Платёж, ₽">
          <Input id="debt-min" inputMode="decimal" placeholder="0" value={minimum} onChange={(e) => setMinimum(amountInput(e.target.value))} className={inputClass} />
        </Field>
        <Field id="debt-day" label="Число">
          <Input id="debt-day" inputMode="numeric" placeholder="10" value={dueDay} onChange={(e) => setDueDay(e.target.value.replace(/\D/g, ""))} className={inputClass} />
        </Field>
      </div>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <PillButton type="submit" className="h-12 w-full" disabled={create.isPending || !name.trim()}>
        Записать долг
      </PillButton>
    </form>
  );
}

function DebtDetails({ debt: d, onDone }: { debt: Debt; onDone: () => void }) {
  const pay = usePayDebt();
  const undo = useCancelLastPayment();
  const update = useUpdateDebt();
  const remove = useDeleteDebt();
  // Empty on purpose: a double tap on a row must not record a payment.
  const [amount, setAmount] = useState("");
  const [balance, setBalance] = useState(String(d.balance).replace(".", ","));
  const [rate, setRate] = useState(String(d.rate).replace(".", ","));
  const [minimum, setMinimum] = useState(String(d.min_payment).replace(".", ","));
  const [payError, setPayError] = useState<string>();
  const [error, setError] = useState<string>();

  async function onPay(event: FormEvent) {
    event.preventDefault();
    const sum = parseAmount(amount);
    if (typeof sum !== "number") {
      setPayError(sum.error);
      return;
    }
    if (sum > d.balance) {
      setPayError(`Осталось отдать только ${formatMoney(d.balance)}`);
      return;
    }
    try {
      const paid = await pay.mutateAsync({ id: d.id, body: { amount: sum } });
      haptic.success();
      setPayError(undefined);
      setAmount("");
      if (paid.balance === 0) {
        toast.success("Долг закрыт", { duration: 10_000, action: { label: "Отменить", onClick: () => undo.mutate(d.id, { onError: fail }) } });
        onDone();
      } else {
        toast.success(`Осталось ${formatMoney(paid.balance)}`);
      }
    } catch (err) {
      if (err instanceof ApiError && err.field) setPayError(err.message);
      else fail(err);
    }
  }

  async function onSave() {
    const left = parseNumber(balance);
    if (left === undefined) {
      setError("Введите остаток");
      return;
    }
    try {
      await update.mutateAsync({
        id: d.id,
        body: { rate: parseNumber(rate) ?? 0, min_payment: parseNumber(minimum) ?? 0, ...(left !== d.balance && { balance: left }) },
      });
      haptic.success();
      onDone();
    } catch (err) {
      if (err instanceof ApiError && err.field) setError(err.message);
      else fail(err);
    }
  }

  async function onDelete() {
    if (!(await confirm(`Удалить долг «${d.name}»? Уже записанные траты останутся.`))) return;
    remove.mutate(d.id, {
      onSuccess: () => {
        haptic.success();
        onDone();
      },
      onError: fail,
    });
  }

  return (
    <div className="flex flex-col gap-5 pb-2">
      <form onSubmit={onPay} noValidate className="flex flex-col gap-3">
        <Field id="pay-amount" label={`Платёж, ₽. Осталось ${formatMoney(d.balance)}`}>
          <Input id="pay-amount" inputMode="decimal" placeholder={d.min_payment > 0 ? String(d.min_payment) : "0"} value={amount} onChange={(e) => setAmount(amountInput(e.target.value))} className={inputClass} />
        </Field>
        <p className="text-[13px] text-muted-foreground">Платёж запишется как трата, поэтому его увидят счета и лимит.</p>
        {payError && (
          <p role="alert" className="text-sm text-destructive">
            {payError}
          </p>
        )}
        <PillButton type="submit" icon={HandCoinsIcon} className="h-12 w-full" disabled={pay.isPending}>
          Записать платёж
        </PillButton>
      </form>

      <Payments debtId={d.id} />

      <div className="flex flex-col gap-3">
        <Field id="fix-balance" label="Остаток по данным банка, ₽">
          <Input id="fix-balance" inputMode="decimal" value={balance} onChange={(e) => setBalance(amountInput(e.target.value))} className={inputClass} />
        </Field>
        <div className="grid grid-cols-2 gap-3">
          <Field id="fix-rate" label="Ставка, %">
            <Input id="fix-rate" inputMode="decimal" value={rate} onChange={(e) => setRate(amountInput(e.target.value))} className={inputClass} />
          </Field>
          <Field id="fix-min" label="Мин. платёж, ₽">
            <Input id="fix-min" inputMode="decimal" value={minimum} onChange={(e) => setMinimum(amountInput(e.target.value))} className={inputClass} />
          </Field>
        </div>
        <p className="text-[13px] text-muted-foreground">Правка остатка не считается тратой: так учитываются проценты банка.</p>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
        <PillButton variant="outline" className="h-12 w-full" disabled={update.isPending} onClick={() => void onSave()}>
          Сохранить
        </PillButton>
        <PillButton variant="outline" className="h-12 w-full text-destructive" disabled={remove.isPending} onClick={() => void onDelete()}>
          Удалить долг
        </PillButton>
      </div>
    </div>
  );
}

function Payments({ debtId }: { debtId: number }) {
  const payments = useDebtPayments(debtId);
  const cancel = useCancelDebtPayment();
  const items = payments.data?.items ?? [];
  if (items.length === 0) return null;
  async function onCancel(id: number) {
    if (!(await confirm("Отменить платёж? Его трата тоже удалится."))) return;
    cancel.mutate(id, { onSuccess: () => haptic.success(), onError: fail });
  }
  return (
    <ul className="flex flex-col gap-1">
      {items.slice(0, 12).map((p) => (
        <li key={p.id} className="flex items-center justify-between gap-3 text-[14px]">
          <span className="text-muted-foreground">{formatDate(p.date)}</span>
          <span className="flex items-center gap-1 tabular-nums">
            {formatMoney(p.amount)}
            <button
              type="button"
              aria-label="Отменить платёж"
              className="flex size-10 items-center justify-center rounded-full text-muted-foreground"
              onClick={() => void onCancel(p.id)}
            >
              <Trash2Icon className="size-4" />
            </button>
          </span>
        </li>
      ))}
    </ul>
  );
}
