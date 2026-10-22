"use client";

import { UserMinusIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import type { Me, User } from "@/api/client";
import { useRemoveMember, useSetWeeklyReport } from "@/api/queries";
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
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { formatDateTime } from "@/lib/format";

export function MembersCard({ users, me }: { users?: User[]; me?: Me }) {
  // The head manages the family; a member only their own settings.
  const canManage = !!me?.is_head;
  const [removing, setRemoving] = useState<User | undefined>();
  const current = users ?? [];

  return (
    <Card>
      <CardHeader>
        <CardTitle>Участники{users && ` · ${current.length}`}</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-6">
        {!users ? (
          <div className="flex flex-col gap-4">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-12 w-full" />
            ))}
          </div>
        ) : current.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            В семье пока никого нет. Создайте приглашение и отправьте ссылку члену семьи.
          </p>
        ) : (
          <ul className="flex flex-col divide-y">
            {current.map((u) => (
              <li key={u.telegram_id} className="flex items-center gap-4 py-3 first:pt-0 last:pb-0">
                <Avatar name={u.first_name} />
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="flex items-center gap-2 font-medium">
                    <span className="truncate">{u.first_name}</span>
                    {u.is_head && <Badge>Глава семьи</Badge>}
                  </span>
                  <span className="truncate text-sm text-muted-foreground">
                    {u.username ? `@${u.username} · ` : ""}в семье с {formatDateTime(u.created_at)}
                  </span>
                </div>
                {(canManage || u.telegram_id === me?.telegram_id) && (
                  <WeeklyReportSwitch member={u} />
                )}
                {canManage && !u.is_head && (
                  <Button
                    variant="ghost"
                    size="icon-lg"
                    aria-label={`Исключить ${u.first_name} из семьи`}
                    className="text-muted-foreground hover:text-destructive"
                    onClick={() => setRemoving(u)}
                  >
                    <UserMinusIcon />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardContent>

      <RemoveMemberDialog member={removing} onClose={() => setRemoving(undefined)} />
    </Card>
  );
}

/** Toggles the Monday summary the bot sends to this member. */
function WeeklyReportSwitch({ member }: { member: User }) {
  const setWeekly = useSetWeeklyReport();
  const id = `weekly-${member.telegram_id}`;
  return (
    <label
      htmlFor={id}
      className="flex shrink-0 cursor-pointer items-center gap-2 text-sm text-muted-foreground"
      title="Бот присылает сводку за прошлую неделю по понедельникам в 10:00"
    >
      <span className="hidden sm:inline">Итоги недели</span>
      <Switch
        id={id}
        aria-label={`Итоги недели для ${member.first_name}`}
        checked={member.weekly_report}
        disabled={setWeekly.isPending}
        onCheckedChange={(on) =>
          setWeekly.mutate(
            { telegramId: member.telegram_id, on },
            {
              onSuccess: () =>
                toast.success(
                  on
                    ? `${member.first_name} будет получать итоги недели`
                    : `Итоги недели для ${member.first_name} отключены`,
                ),
              onError: () => toast.error("Не удалось изменить настройку"),
            },
          )
        }
      />
    </label>
  );
}

function Avatar({ name }: { name: string }) {
  return (
    <span
      aria-hidden
      className="flex size-10 shrink-0 items-center justify-center rounded-full bg-primary/10 font-medium text-primary"
    >
      {name.charAt(0).toUpperCase()}
    </span>
  );
}

function RemoveMemberDialog({ member, onClose }: { member?: User; onClose: () => void }) {
  const remove = useRemoveMember();

  async function confirm() {
    if (!member) return;
    try {
      await remove.mutateAsync(member.telegram_id);
      toast.success(`${member.first_name} больше не в семье`);
      onClose();
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Не удалось исключить участника");
    }
  }

  return (
    <AlertDialog open={!!member} onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Исключить {member?.first_name} из семьи?</AlertDialogTitle>
          <AlertDialogDescription>
            Человек продолжит вести расходы сам и заберёт свои траты — вы перестанете их видеть.
            Вернуть можно новым приглашением.
          </AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel className="h-10">Отмена</AlertDialogCancel>
          <AlertDialogAction
            variant="destructive"
            className="h-10"
            disabled={remove.isPending}
            onClick={(e) => {
              e.preventDefault();
              void confirm();
            }}
          >
            {remove.isPending ? "Исключение…" : "Исключить"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
