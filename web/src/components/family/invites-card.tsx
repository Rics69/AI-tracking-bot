"use client";

import { CheckIcon, CopyIcon, LinkIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";

import { ApiError, type CreatedInvite, type Invite, type User } from "@/api/client";
import { useCreateInvite, useRevokeInvite } from "@/api/queries";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { formatDateTime } from "@/lib/format";

type Props = { invites?: Invite[]; users: User[] };

export function InvitesCard({ invites, users }: Props) {
  const create = useCreateInvite();
  const revoke = useRevokeInvite();
  const [created, setCreated] = useState<CreatedInvite | undefined>();
  const names = new Map(users.map((u) => [u.telegram_id, u.first_name]));

  async function onCreate() {
    try {
      setCreated(await create.mutateAsync());
    } catch (err) {
      toast.error(
        err instanceof ApiError && err.status === 409
          ? "Бот ещё ни разу не запускался, поэтому ссылку на него не собрать. Запустите бота и попробуйте снова."
          : "Не удалось создать приглашение",
      );
    }
  }

  async function onRevoke(id: number) {
    try {
      await revoke.mutateAsync(id);
      toast.success("Приглашение отозвано");
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Не удалось отозвать приглашение");
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Приглашения</CardTitle>
        <CardAction>
          <Button className="h-10" disabled={create.isPending} onClick={() => void onCreate()}>
            <LinkIcon />
            Пригласить
          </Button>
        </CardAction>
      </CardHeader>
      <CardContent>
        {!invites ? (
          <Skeleton className="h-16 w-full" />
        ) : invites.length === 0 ? (
          <p className="text-sm text-muted-foreground">
            Ссылка-приглашение открывает бота и сразу добавляет человека в семью. Действует 7 дней,
            срабатывает один раз.
          </p>
        ) : (
          <ul className="flex flex-col divide-y">
            {invites.map((inv) => (
              <li key={inv.id} className="flex items-center gap-3 py-3 first:pt-0 last:pb-0">
                <div className="flex min-w-0 flex-1 flex-col gap-1">
                  <StatusBadge
                    invite={inv}
                    usedBy={inv.used_by ? names.get(inv.used_by) : undefined}
                  />
                  <span className="text-sm text-muted-foreground">
                    создано {formatDateTime(inv.created_at)}
                    {inv.status === "active" && ` · до ${formatDateTime(inv.expires_at)}`}
                  </span>
                </div>
                {inv.status === "active" && (
                  <Button
                    variant="ghost"
                    size="icon-lg"
                    aria-label="Отозвать приглашение"
                    className="text-muted-foreground hover:text-destructive"
                    disabled={revoke.isPending}
                    onClick={() => void onRevoke(inv.id)}
                  >
                    <XIcon />
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
      </CardContent>

      <CreatedInviteDialog invite={created} onClose={() => setCreated(undefined)} />
    </Card>
  );
}

function StatusBadge({ invite, usedBy }: { invite: Invite; usedBy?: string }) {
  switch (invite.status) {
    case "active":
      return <Badge className="w-fit">Ждёт ответа</Badge>;
    case "used":
      return (
        <Badge variant="secondary" className="w-fit">
          Принято{usedBy ? `: ${usedBy}` : ""}
        </Badge>
      );
    case "expired":
      return (
        <Badge variant="outline" className="w-fit">
          Истекло
        </Badge>
      );
    case "revoked":
      return (
        <Badge variant="outline" className="w-fit">
          Отозвано
        </Badge>
      );
  }
}

function CreatedInviteDialog({ invite, onClose }: { invite?: CreatedInvite; onClose: () => void }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    if (!invite) return;
    try {
      await navigator.clipboard.writeText(invite.link);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      toast.error("Не удалось скопировать — выделите ссылку вручную");
    }
  }

  return (
    <Dialog open={!!invite} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Приглашение готово</DialogTitle>
          <DialogDescription>
            Отправьте ссылку члену семьи. Она откроет бота, и после нажатия «Start» человек попадёт
            в семью. Ссылка одноразовая, действует до {invite && formatDateTime(invite.expires_at)}{" "}
            и показывается только сейчас.
          </DialogDescription>
        </DialogHeader>
        <div className="flex gap-2">
          <Input
            readOnly
            value={invite?.link ?? ""}
            aria-label="Ссылка-приглашение"
            className="h-10 font-mono text-sm"
            onFocus={(e) => e.target.select()}
          />
          <Button className="h-10 shrink-0" onClick={() => void copy()}>
            {copied ? <CheckIcon /> : <CopyIcon />}
            {copied ? "Скопировано" : "Копировать"}
          </Button>
        </div>
        <DialogFooter>
          <Button variant="outline" className="h-10" onClick={onClose}>
            Готово
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
