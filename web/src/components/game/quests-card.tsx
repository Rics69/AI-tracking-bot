"use client";

import { CheckIcon, FlameIcon, ShareIcon, SwordsIcon } from "lucide-react";
import { toast } from "sonner";

import type { CardRequest, Quest } from "@/api/client";
import { createCard, useDoneQuest, useGame } from "@/api/queries";
import { Panel, Pill, PillButton } from "@/components/kit";
import { Skeleton } from "@/components/ui/skeleton";
import { plural } from "@/lib/format";
import { haptic, shareStory } from "@/lib/telegram";

/** Opens the story editor with a card; money never gets on it. */
export async function shareCard(body: CardRequest) {
  try {
    const card = await createCard(body);
    if (!shareStory(card.url, card.text)) toast.error("Истории доступны в Telegram 7.8 и новее");
  } catch (err) {
    toast.error(err instanceof Error ? err.message : "Не удалось подготовить карточку");
  }
}

/** The level, the streak and the quests of today. */
export function QuestsCard() {
  const game = useGame();
  const g = game.data;
  return (
    <Panel
      icon={SwordsIcon}
      title="Квесты"
      action={g && <Pill tone="soft">уровень {g.level}</Pill>}
    >
      {!g ? (
        <Skeleton className="h-20 w-full rounded-2xl" />
      ) : (
        <>
          <div className="flex items-center gap-2">
            <Pill tone={g.streak > 0 ? "warning" : "neutral"}>
              <FlameIcon className="mr-1 size-3.5" aria-hidden />
              {g.streak} {plural(g.streak, ["день", "дня", "дней"])} подряд
            </Pill>
            <Pill>{g.xp} опыта</Pill>
          </div>
          <ul className="flex flex-col gap-3">
            {g.quests.slice(0, 3).map((q) => (
              <QuestRow key={q.slug} quest={q} />
            ))}
          </ul>
          <div className="flex gap-2">
            <PillButton variant="outline" icon={ShareIcon} className="h-10 flex-1" onClick={() => void shareCard({ kind: "level" })}>
              Уровень
            </PillButton>
            <PillButton
              variant="outline"
              icon={ShareIcon}
              className="h-10 flex-1"
              disabled={g.streak === 0}
              onClick={() => void shareCard({ kind: "streak" })}
            >
              Серия
            </PillButton>
          </div>
        </>
      )}
    </Panel>
  );
}

function QuestRow({ quest: q }: { quest: Quest }) {
  const done = useDoneQuest();
  return (
    <li className="flex items-center justify-between gap-3">
      <span className="flex min-w-0 flex-col">
        <span className="text-[15px] font-medium">{q.title}</span>
        <span className="text-[13px] text-muted-foreground">{q.text}</span>
      </span>
      {q.done ? (
        <CheckIcon className="size-5 shrink-0 text-success" aria-label="Сделано" />
      ) : q.manual ? (
        <PillButton
          className="h-9 px-4 text-[14px]"
          disabled={done.isPending}
          onClick={() =>
            done.mutate(q.slug, {
              onSuccess: () => {
                haptic.success();
                toast.success(`+${q.xp} опыта`);
              },
              onError: (err) => toast.error(err.message),
            })
          }
        >
          Сделал
        </PillButton>
      ) : (
        <Pill>+{q.xp}</Pill>
      )}
    </li>
  );
}
