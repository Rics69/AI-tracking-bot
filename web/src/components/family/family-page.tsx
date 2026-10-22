"use client";

import { AlertCircleIcon } from "lucide-react";

import { useInvites, useMe, useUsers } from "@/api/queries";

import { InvitesCard } from "./invites-card";
import { MembersCard } from "./members-card";

export function FamilyPage() {
  const me = useMe();
  const users = useUsers();
  const invites = useInvites();
  const error = me.error ?? users.error ?? invites.error;

  if (error) {
    return (
      <div
        role="alert"
        className="flex items-center gap-3 rounded-xl p-6 ring-1 ring-destructive/30"
      >
        <AlertCircleIcon className="size-5 shrink-0 text-destructive" />
        <div>
          <p className="font-medium">Не удалось загрузить семью</p>
          <p className="text-sm text-muted-foreground">{error.message}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="grid gap-6 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)] lg:items-start">
      <MembersCard users={users.data} me={me.data} />
      {/* Only the head invites people. */}
      {me.data?.is_head && <InvitesCard invites={invites.data} users={users.data ?? []} />}
    </div>
  );
}
