"use client";

import { UserMinusIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import type { User } from "@/api/client";
import { useRemoveMember } from "@/api/queries";
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
import { formatDateTime } from "@/lib/format";

export function MembersCard({ users }: { users?: User[] }) {
  const [removing, setRemoving] = useState<User | undefined>();
  const current = users?.filter((u) => !u.removed_at) ?? [];
  const former = users?.filter((u) => u.removed_at) ?? [];

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
                {!u.is_head && (
                  <Button
                    variant="ghost"
                    size="icon-lg"
                    aria-label={`Удалить ${u.first_name} из семьи`}
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

        {former.length > 0 && (
          <div className="flex flex-col gap-2">
            <p className="text-sm font-medium text-muted-foreground">Бывшие участники</p>
            <ul className="flex flex-col gap-1 text-sm text-muted-foreground">
              {former.map((u) => (
                <li key={u.telegram_id}>
                  {u.first_name} — удалён {formatDateTime(u.removed_at!)}, траты сохранены
                </li>
              ))}
            </ul>
          </div>
        )}
      </CardContent>

      <RemoveMemberDialog member={removing} onClose={() => setRemoving(undefined)} />
    </Card>
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
      toast.error(err instanceof Error ? err.message : "Не удалось удалить участника");
    }
  }

  return (
    <AlertDialog open={!!member} onOpenChange={(open) => !open && onClose()}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>Удалить {member?.first_name} из семьи?</AlertDialogTitle>
          <AlertDialogDescription>
            Бот перестанет отвечать этому человеку. Его траты останутся в истории. Вернуть можно
            новым приглашением.
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
            {remove.isPending ? "Удаление…" : "Удалить"}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
