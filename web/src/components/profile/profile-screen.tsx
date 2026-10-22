"use client";

import { useQueryClient } from "@tanstack/react-query";
import { CrownIcon, LogOutIcon, SendIcon, UserMinusIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import type { Me, Member } from "@/api/client";
import {
  useCreateInvite,
  useDeleteMe,
  useFamily,
  useLeaveFamily,
  useMe,
  useRemoveMember,
  useRevokeInvite,
  useTransferHead,
  useUpdateMe,
} from "@/api/queries";
import { confirm } from "@/components/operations/operation-sheet";
import { Button } from "@/components/ui/button";
import { Sheet } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { texts } from "@/content/texts";
import { formatDateTime } from "@/lib/format";
import { closeApp, haptic, shareLink } from "@/lib/telegram";
import { timezoneOptions } from "@/lib/timezones";
import { cn } from "@/lib/utils";

export function ProfileScreen() {
  const me = useMe();
  if (!me.data) return <Skeleton className="h-40 w-full rounded-2xl" />;
  const u = me.data;
  return (
    <>
      <header className="flex items-center gap-4 px-1">
        <Avatar name={u.first_name} size="lg" />
        <div className="flex min-w-0 flex-col">
          <h1 className="truncate text-2xl font-semibold tracking-tight">{u.first_name}</h1>
          {u.username && (
            <span className="truncate text-[15px] text-muted-foreground">@{u.username}</span>
          )}
        </div>
      </header>
      <FamilySection me={u} />
      <SettingsSection me={u} />
      <PrivacySection />
      <DeleteAccount me={u} />
    </>
  );
}

// Family.

function FamilySection({ me }: { me: Me }) {
  const family = useFamily();
  const [selected, setSelected] = useState<Member | null>(null);
  const members = family.data?.members;
  const alone = members?.length === 1;

  return (
    <Section title="Семья">
      {!members ? (
        <Skeleton className="h-24 w-full" />
      ) : (
        <>
          {alone ? (
            <p className="px-4 pt-3 text-[15px] text-muted-foreground">{texts.familyAlone}</p>
          ) : (
            <ul className="flex flex-col">
              {members.map((m) => {
                const self = m.telegram_id === me.telegram_id;
                const manageable = me.is_head && !self;
                const row = (
                  <>
                    <Avatar name={m.first_name} />
                    <span className="flex min-w-0 flex-1 flex-col text-left">
                      <span className="truncate text-[15px] font-medium">
                        {m.first_name}
                        {self && <span className="font-normal text-muted-foreground"> · вы</span>}
                      </span>
                      <span className="text-[13px] text-muted-foreground">
                        {m.is_head ? "Глава семьи" : "Участник"}
                      </span>
                    </span>
                    {m.is_head && (
                      <CrownIcon className="size-4 text-muted-foreground" aria-hidden />
                    )}
                  </>
                );
                return (
                  <li key={m.telegram_id}>
                    {manageable ? (
                      <button
                        type="button"
                        onClick={() => setSelected(m)}
                        className="flex min-h-14 w-full items-center gap-3 px-4 py-2 active:bg-muted/60"
                      >
                        {row}
                      </button>
                    ) : (
                      <div className="flex min-h-14 items-center gap-3 px-4 py-2">{row}</div>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
          {!alone && !me.is_head && (
            <p className="px-4 pt-1 text-[13px] text-muted-foreground">{texts.familyMember}</p>
          )}
          <div className="flex flex-col gap-2 p-4">
            {me.is_head ? <InviteButton /> : <LeaveButton />}
            {me.is_head && <ActiveInvites invites={family.data?.invites ?? []} />}
          </div>
        </>
      )}
      <MemberSheet member={selected} onClose={() => setSelected(null)} />
    </Section>
  );
}

function InviteButton() {
  const create = useCreateInvite();
  return (
    <Button
      size="lg"
      className="h-12 text-base"
      disabled={create.isPending}
      onClick={() =>
        create.mutate(undefined, {
          onSuccess: async (inv) => {
            haptic.success();
            // In Telegram the share dialog sends the link to a chat; outside, copy it.
            if (!shareLink(inv.link, texts.inviteShareText)) {
              await navigator.clipboard?.writeText(inv.link).catch(() => undefined);
              toast.success("Ссылка скопирована", { description: inv.link });
            }
          },
          onError: (err) => toast.error(err.message),
        })
      }
    >
      <SendIcon className="size-5" />
      Пригласить в семью
    </Button>
  );
}

function ActiveInvites({
  invites,
}: {
  invites: { id: number; status: string; expires_at: string }[];
}) {
  const revoke = useRevokeInvite();
  const active = invites.filter((i) => i.status === "active");
  if (active.length === 0) return null;
  return (
    <ul className="flex flex-col gap-1">
      {active.map((i) => (
        <li
          key={i.id}
          className="flex items-center justify-between gap-2 text-[13px] text-muted-foreground"
        >
          <span>Ссылка ждёт до {formatDateTime(i.expires_at)}</span>
          <button
            type="button"
            aria-label="Отозвать ссылку"
            className="flex size-11 items-center justify-center rounded-lg"
            disabled={revoke.isPending}
            onClick={() => revoke.mutate(i.id, { onError: (err) => toast.error(err.message) })}
          >
            <XIcon className="size-4" />
          </button>
        </li>
      ))}
    </ul>
  );
}

function LeaveButton() {
  const leave = useLeaveFamily();
  return (
    <Button
      variant="secondary"
      size="lg"
      className="h-12 text-base"
      disabled={leave.isPending}
      onClick={async () => {
        if (
          !(await confirm(
            "Выйти из семьи? Ваши траты останутся с вами, глава перестанет их видеть.",
          ))
        )
          return;
        leave.mutate(undefined, {
          onSuccess: () => {
            haptic.success();
            toast.success("Вы вышли из семьи");
          },
          onError: (err) => toast.error(err.message),
        });
      }}
    >
      <LogOutIcon className="size-5" />
      Выйти из семьи
    </Button>
  );
}

/** What the head can do with a member. */
function MemberSheet({ member, onClose }: { member: Member | null; onClose: () => void }) {
  const remove = useRemoveMember();
  const transfer = useTransferHead();
  const pending = remove.isPending || transfer.isPending;
  const name = member?.first_name ?? "";
  return (
    <Sheet open={member !== null} onOpenChange={(open) => !open && onClose()} title={name}>
      {member && (
        <div className="flex flex-col gap-2 pb-2">
          <Button
            variant="secondary"
            size="lg"
            className="h-12 justify-start text-base"
            disabled={pending}
            onClick={async () => {
              if (
                !(await confirm(
                  `Сделать главой семьи: ${name}? Вы станете участником и будете видеть только свои траты.`,
                ))
              )
                return;
              transfer.mutate(member.telegram_id, {
                onSuccess: () => {
                  haptic.success();
                  toast.success(`${name} теперь глава семьи`);
                  onClose();
                },
                onError: (err) => toast.error(err.message),
              });
            }}
          >
            <CrownIcon className="size-5" />
            Сделать главой семьи
          </Button>
          <Button
            variant="destructive"
            size="lg"
            className="h-12 justify-start text-base"
            disabled={pending}
            onClick={async () => {
              if (
                !(await confirm(
                  `Исключить из семьи: ${name}? Траты останутся у этого человека, вы перестанете их видеть.`,
                ))
              )
                return;
              remove.mutate(member.telegram_id, {
                onSuccess: () => {
                  haptic.success();
                  toast.success(`${name} больше не в семье`);
                  onClose();
                },
                onError: (err) => toast.error(err.message),
              });
            }}
          >
            <UserMinusIcon className="size-5" />
            Исключить из семьи
          </Button>
        </div>
      )}
    </Sheet>
  );
}

// Settings.

function SettingsSection({ me }: { me: Me }) {
  const update = useUpdateMe();
  const zones = timezoneOptions(me.effective_timezone);
  const save = (body: { weekly_report?: boolean; timezone?: string }) =>
    update.mutate(body, {
      onSuccess: () => haptic.select(),
      onError: (err) => toast.error(err.message),
    });
  return (
    <Section title="Настройки">
      <label className="flex min-h-14 items-center justify-between gap-4 px-4 py-2">
        <span className="flex flex-col">
          <span className="text-[15px]">Итоги недели</span>
          <span className="text-[13px] text-muted-foreground">
            Бот присылает сводку по понедельникам
          </span>
        </span>
        <Switch
          checked={me.weekly_report}
          disabled={update.isPending}
          onCheckedChange={(on) => save({ weekly_report: on })}
        />
      </label>
      <label className="flex min-h-14 items-center justify-between gap-4 border-t px-4 py-2">
        <span className="flex flex-col">
          <span className="text-[15px]">Часовой пояс</span>
          <span className="text-[13px] text-muted-foreground">
            От него зависит, какой день «сегодня»
          </span>
        </span>
        <select
          value={me.effective_timezone}
          disabled={update.isPending}
          onChange={(e) => save({ timezone: e.target.value })}
          className="h-10 max-w-40 rounded-lg bg-muted px-2 text-[15px] outline-none"
        >
          {zones.map((z) => (
            <option key={z.value} value={z.value}>
              {z.label}
            </option>
          ))}
        </select>
      </label>
    </Section>
  );
}

function PrivacySection() {
  return (
    <Section title="Данные">
      <details className="group px-4 py-3">
        <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between text-[15px]">
          Как хранятся данные
          <span className="text-muted-foreground transition-transform group-open:rotate-90">›</span>
        </summary>
        <ul className="flex list-disc flex-col gap-2 pt-2 pl-5 text-[15px] text-muted-foreground">
          {texts.privacy.map((line) => (
            <li key={line}>{line}</li>
          ))}
        </ul>
      </details>
    </Section>
  );
}

function DeleteAccount({ me }: { me: Me }) {
  const del = useDeleteMe();
  const family = useFamily();
  const qc = useQueryClient();
  const headWithMembers = me.is_head && (family.data?.members.length ?? 0) > 1;
  return (
    <div className="flex flex-col gap-2 px-1 pb-4">
      <Button
        variant="ghost"
        className="h-11 text-destructive"
        disabled={del.isPending}
        onClick={async () => {
          const warning =
            texts.deleteAccountWarning +
            (headWithMembers ? "\n\n" + texts.deleteAccountHeadWarning : "");
          if (!(await confirm(`Удалить аккаунт?\n\n${warning}`))) return;
          del.mutate(undefined, {
            onSuccess: () => {
              haptic.success();
              closeApp();
              qc.clear(); // outside Telegram the app starts over at the consent screen
            },
            onError: (err) => toast.error(err.message),
          });
        }}
      >
        Удалить аккаунт
      </Button>
    </div>
  );
}

// Pieces.

function Section({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="px-1 text-sm font-medium text-muted-foreground">{title}</h2>
      <div className="flex flex-col overflow-hidden rounded-2xl bg-card">{children}</div>
    </section>
  );
}

function Avatar({ name, size = "md" }: { name: string; size?: "md" | "lg" }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex shrink-0 items-center justify-center rounded-full bg-primary/15 font-semibold text-primary",
        size === "lg" ? "size-16 text-2xl" : "size-10 text-base",
      )}
    >
      {name.charAt(0).toUpperCase()}
    </span>
  );
}
