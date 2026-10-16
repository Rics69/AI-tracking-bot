import type { User } from "@/api/client";

/** "Данил (@rics69)" or just the first name; removed members are marked. */
export function memberName(u: User): string {
  const name = u.username ? `${u.first_name} (@${u.username})` : u.first_name;
  return u.removed_at ? `${name} — удалён` : name;
}

/** Current family members: only they can get new expenses. */
export function activeMembers(users: User[]): User[] {
  return users.filter((u) => !u.removed_at);
}
