"use client";

import {
  ArrowLeftRightIcon,
  BanknoteIcon,
  CreditCardIcon,
  type LucideIcon,
  PiggyBankIcon,
  PlusIcon,
  Trash2Icon,
  WalletIcon,
} from "lucide-react";
import { useRouter } from "next/navigation";
import { useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";

import { ApiError, type Account, type AccountKind, type Currency } from "@/api/client";
import {
  useAccounts,
  useCreateAccount,
  useCreateTransfer,
  useDeleteTransfer,
  useTransfers,
  useUpdateAccount,
} from "@/api/queries";
import { Card, IconCircle, PageHeader, Panel, Pill, PillButton } from "@/components/kit";
import { confirm } from "@/components/operations/operation-sheet";
import { Input } from "@/components/ui/input";
import { Sheet, useTelegramBack } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { amountInput, parseAmount } from "@/lib/amount";
import { currencySign, formatDate, formatForeign, formatMoney, plural } from "@/lib/format";
import { haptic } from "@/lib/telegram";

const kinds: { value: AccountKind; label: string; icon: LucideIcon }[] = [
  { value: "card", label: "Карта", icon: CreditCardIcon },
  { value: "cash", label: "Наличные", icon: BanknoteIcon },
  { value: "savings", label: "Накопительный", icon: PiggyBankIcon },
  { value: "foreign", label: "Валютный", icon: WalletIcon },
  { value: "other", label: "Другое", icon: WalletIcon },
];

const currencies: Currency[] = ["RUB", "USD", "EUR"];

function kindOf(k: AccountKind) {
  return kinds.find((x) => x.value === k) ?? kinds[0];
}

export function fail(err: unknown) {
  haptic.error();
  toast.error(err instanceof Error ? err.message : "Не удалось сохранить");
}

export function AccountsScreen() {
  const router = useRouter();
  useTelegramBack(true, () => router.back());
  const accounts = useAccounts();
  const [adding, setAdding] = useState(false);
  const [moving, setMoving] = useState(false);
  const [editing, setEditing] = useState<Account | null>(null);
  const items = accounts.data?.items;
  return (
    <>
      <PageHeader title="Счета" />
      <Card className="flex flex-col gap-1 border-transparent bg-hero p-5 text-hero-foreground">
        <span className="text-[14px] opacity-80">Всего на счетах</span>
        {accounts.data ? (
          <span className="text-[34px] font-semibold tracking-tight text-gold tabular-nums">
            {(accounts.data.total_rub < 0 ? "−" : "") + formatMoney(Math.abs(accounts.data.total_rub))}
          </span>
        ) : (
          <Skeleton className="h-10 w-40 bg-hero-foreground/15" />
        )}
        <span className="text-[13px] opacity-70">Валюта — по последнему курсу ЦБ</span>
      </Card>

      <div className="flex gap-2">
        <PillButton icon={PlusIcon} className="flex-1" onClick={() => setAdding(true)}>
          Счёт
        </PillButton>
        <PillButton
          variant="outline"
          icon={ArrowLeftRightIcon}
          className="flex-1"
          disabled={(items?.length ?? 0) < 2}
          onClick={() => setMoving(true)}
        >
          Перевод
        </PillButton>
      </div>

      <Panel icon={WalletIcon} title="Мои счета">
        {!items ? (
          <Skeleton className="h-28 w-full rounded-2xl" />
        ) : (
          <ul className="flex flex-col">
            {items.map((a) => {
              const kind = kindOf(a.kind);
              return (
                <li key={a.id}>
                  <button
                    type="button"
                    onClick={() => setEditing(a)}
                    className="flex min-h-14 w-full items-center gap-3 rounded-2xl px-1 py-1.5 text-left active:bg-muted"
                  >
                    <IconCircle icon={kind.icon} className="size-10" />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate text-[15px] font-medium">{a.name}</span>
                      <span className="text-[13px] text-muted-foreground">{kind.label}</span>
                    </span>
                    <span className="flex flex-col items-end text-[15px] font-medium tabular-nums">
                      {formatForeign(a.balance, a.currency)}
                      {a.currency !== "RUB" && (
                        <span className="text-[12px] font-normal text-muted-foreground">{formatMoney(a.balance_rub)}</span>
                      )}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </Panel>

      <TransfersPanel accounts={items ?? []} />

      <Sheet open={adding} onOpenChange={setAdding} title="Новый счёт" description="Название, вид, валюта и остаток">
        {adding && <AccountForm onDone={() => setAdding(false)} />}
      </Sheet>
      <Sheet open={moving} onOpenChange={setMoving} title="Перевод" description="Между своими счетами">
        {moving && items && <TransferForm accounts={items} onDone={() => setMoving(false)} />}
      </Sheet>
      <Sheet open={editing !== null} onOpenChange={(o) => !o && setEditing(null)} title="Счёт" description="Название и остаток">
        {editing && <AccountEdit key={editing.id} account={editing} onDone={() => setEditing(null)} />}
      </Sheet>
    </>
  );
}

function TransfersPanel({ accounts }: { accounts: Account[] }) {
  const transfers = useTransfers();
  const remove = useDeleteTransfer();
  const items = transfers.data?.items ?? [];
  if (items.length === 0) return null;
  const find = (id: number) => accounts.find((a) => a.id === id);
  async function onDelete(id: number) {
    if (!(await confirm("Отменить перевод?"))) return;
    remove.mutate(id, { onSuccess: () => haptic.success(), onError: fail });
  }
  return (
    <Panel
      icon={ArrowLeftRightIcon}
      title="Переводы"
      action={
        <Pill>
          {items.length} {plural(items.length, ["запись", "записи", "записей"])}
        </Pill>
      }
    >
      <ul className="flex flex-col gap-3">
        {items.slice(0, 20).map((t) => {
          const from = find(t.from_account_id);
          const to = find(t.to_account_id);
          return (
            <li key={t.id} className="flex items-center justify-between gap-3">
              <span className="flex min-w-0 flex-col">
                <span className="truncate text-[15px] font-medium">
                  {from?.name ?? "Счёт"} → {to?.name ?? "Счёт"}
                </span>
                <span className="text-[13px] text-muted-foreground">{formatDate(t.date)}</span>
              </span>
              <span className="flex shrink-0 items-center gap-2">
                <span className="flex flex-col items-end text-[15px] font-medium tabular-nums">
                  {formatForeign(t.amount_from, from?.currency ?? "RUB")}
                  {from?.currency !== to?.currency && (
                    <span className="text-[12px] font-normal text-muted-foreground">
                      {formatForeign(t.amount_to, to?.currency ?? "RUB")}
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  aria-label="Отменить перевод"
                  className="flex size-10 items-center justify-center rounded-full text-muted-foreground"
                  onClick={() => void onDelete(t.id)}
                >
                  <Trash2Icon className="size-4" />
                </button>
              </span>
            </li>
          );
        })}
      </ul>
    </Panel>
  );
}

export function Field({ id, label, children }: { id: string; label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <label htmlFor={id} className="text-sm font-medium text-muted-foreground">
        {label}
      </label>
      {children}
    </div>
  );
}

export const inputClass = "h-11 rounded-full px-4 text-[15px]";
export const selectClass = "h-11 min-w-0 rounded-full bg-muted px-4 text-[15px] outline-none";

function AccountForm({ onDone }: { onDone: () => void }) {
  const create = useCreateAccount();
  const [name, setName] = useState("");
  const [kind, setKind] = useState<AccountKind>("card");
  const [currency, setCurrency] = useState<Currency>("RUB");
  const [balance, setBalance] = useState("");
  const [error, setError] = useState<string>();

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const amount = balance.trim() ? parseAmount(balance) : 0;
    if (typeof amount !== "number") {
      setError(amount.error);
      return;
    }
    try {
      await create.mutateAsync({ name: name.trim(), kind, currency, balance: amount });
      haptic.success();
      onDone();
    } catch (err) {
      if (err instanceof ApiError && err.field) setError(err.message);
      else fail(err);
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4 pb-2">
      <Field id="acc-name" label="Название">
        <Input id="acc-name" maxLength={40} placeholder="Наличные" value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </Field>
      <div className="grid grid-cols-2 gap-3">
        <Field id="acc-kind" label="Вид">
          <select id="acc-kind" value={kind} onChange={(e) => setKind(e.target.value as AccountKind)} className={selectClass}>
            {kinds.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </select>
        </Field>
        <Field id="acc-currency" label="Валюта">
          <select id="acc-currency" value={currency} onChange={(e) => setCurrency(e.target.value as Currency)} className={selectClass}>
            {currencies.map((c) => (
              <option key={c} value={c}>
                {c} {currencySign(c)}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <Field id="acc-balance" label="Сколько на счёте сейчас">
        <Input id="acc-balance" inputMode="decimal" placeholder="0" value={balance} onChange={(e) => setBalance(amountInput(e.target.value))} className={inputClass} />
      </Field>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <PillButton type="submit" className="h-12 w-full" disabled={create.isPending || !name.trim()}>
        Открыть счёт
      </PillButton>
    </form>
  );
}

function AccountEdit({ account: a, onDone }: { account: Account; onDone: () => void }) {
  const update = useUpdateAccount();
  const [name, setName] = useState(a.name);
  const [balance, setBalance] = useState(String(a.balance).replace(".", ","));
  const [error, setError] = useState<string>();

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const amount = Number(balance.replace(/\s/g, "").replace(",", "."));
    if (Number.isNaN(amount)) {
      setError("Введите сумму");
      return;
    }
    try {
      await update.mutateAsync({ id: a.id, body: { name: name.trim(), ...(amount !== a.balance && { balance: amount }) } });
      haptic.success();
      onDone();
    } catch (err) {
      fail(err);
    }
  }

  async function onArchive() {
    if (!(await confirm(`Скрыть счёт «${a.name}»? Операции и переводы останутся.`))) return;
    try {
      await update.mutateAsync({ id: a.id, body: { archived: true } });
      haptic.success();
      onDone();
    } catch (err) {
      fail(err);
    }
  }

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4 pb-2">
      <Field id="edit-name" label="Название">
        <Input id="edit-name" maxLength={40} value={name} onChange={(e) => setName(e.target.value)} className={inputClass} />
      </Field>
      <Field id="edit-balance" label={`Остаток, ${currencySign(a.currency)}`}>
        <Input id="edit-balance" inputMode="decimal" value={balance} onChange={(e) => setBalance(e.target.value.replace(/[^\d\s,.-]/g, ""))} className={inputClass} />
      </Field>
      <p className="text-[13px] text-muted-foreground">Правка остатка не считается тратой или доходом.</p>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <PillButton type="submit" className="h-12 w-full" disabled={update.isPending || !name.trim()}>
        Сохранить
      </PillButton>
      {!a.is_default && (
        <PillButton variant="outline" className="h-12 w-full text-destructive" disabled={update.isPending} onClick={() => void onArchive()}>
          Скрыть счёт
        </PillButton>
      )}
    </form>
  );
}

function TransferForm({ accounts, onDone }: { accounts: Account[]; onDone: () => void }) {
  const create = useCreateTransfer();
  const [fromId, setFromId] = useState(accounts[0].id);
  const [toId, setToId] = useState(accounts[1].id);
  const [amountFrom, setAmountFrom] = useState("");
  const [amountTo, setAmountTo] = useState("");
  const [error, setError] = useState<string>();
  const from = accounts.find((a) => a.id === fromId)!;
  const to = accounts.find((a) => a.id === toId)!;
  const differ = from.currency !== to.currency;

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const sent = parseAmount(amountFrom);
    const got = differ ? parseAmount(amountTo) : sent;
    if (typeof sent !== "number" || typeof got !== "number") {
      setError("Введите сумму");
      return;
    }
    if (fromId === toId) {
      setError("Выберите два разных счёта");
      return;
    }
    try {
      await create.mutateAsync({
        from_account_id: fromId,
        to_account_id: toId,
        amount_from: sent,
        ...(differ && { amount_to: got }),
      });
      haptic.success();
      toast.success("Перевод записан");
      onDone();
    } catch (err) {
      if (err instanceof ApiError && err.field) setError(err.message);
      else fail(err);
    }
  }

  const picker = (id: string, value: number, onChange: (v: number) => void) => (
    <select id={id} value={value} onChange={(e) => onChange(Number(e.target.value))} className={selectClass}>
      {accounts.map((a) => (
        <option key={a.id} value={a.id}>
          {a.name}
        </option>
      ))}
    </select>
  );

  return (
    <form onSubmit={onSubmit} noValidate className="flex flex-col gap-4 pb-2">
      <div className="grid grid-cols-2 gap-3">
        <Field id="tr-from" label="Откуда">
          {picker("tr-from", fromId, setFromId)}
        </Field>
        <Field id="tr-to" label="Куда">
          {picker("tr-to", toId, setToId)}
        </Field>
      </div>
      <Field id="tr-amount" label={`Сумма, ${currencySign(from.currency)}`}>
        <Input id="tr-amount" inputMode="decimal" placeholder="0" value={amountFrom} onChange={(e) => setAmountFrom(amountInput(e.target.value))} className={inputClass} />
      </Field>
      {differ && (
        <Field id="tr-amount-to" label={`Пришло, ${currencySign(to.currency)}`}>
          <Input id="tr-amount-to" inputMode="decimal" placeholder="0" value={amountTo} onChange={(e) => setAmountTo(amountInput(e.target.value))} className={inputClass} />
        </Field>
      )}
      <p className="text-[13px] text-muted-foreground">Перевод не считается тратой и не меняет лимит.</p>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <PillButton type="submit" className="h-12 w-full" disabled={create.isPending}>
        Записать
      </PillButton>
    </form>
  );
}
