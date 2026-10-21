import type { User } from "@/api/client";

/** "Данил (@rics69)" or just the first name. */
export function memberName(u: User): string {
  return u.username ? `${u.first_name} (@${u.username})` : u.first_name;
}
