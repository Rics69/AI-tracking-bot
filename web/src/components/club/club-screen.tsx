"use client";

import { BookOpenIcon, CrownIcon, LockIcon } from "lucide-react";
import { useRouter } from "next/navigation";
import { useState, type FormEvent, type ReactNode } from "react";
import { toast } from "sonner";

import { useClub, useGuide, useGuides, useRedeemPromo } from "@/api/queries";
import { Card, PageHeader, Panel, Pill, PillButton } from "@/components/kit";
import { Input } from "@/components/ui/input";
import { Sheet, useTelegramBack } from "@/components/ui/sheet";
import { Skeleton } from "@/components/ui/skeleton";
import { texts } from "@/content/texts";
import { formatDate } from "@/lib/format";
import { haptic } from "@/lib/telegram";

export function ClubScreen() {
  const router = useRouter();
  useTelegramBack(true, () => router.back());
  const club = useClub().data;
  const guides = useGuides().data?.items;
  const [open, setOpen] = useState<string | null>(null);
  return (
    <>
      <PageHeader title="Клуб" />
      <Card className="flex flex-col gap-2 border-transparent bg-hero p-5 text-hero-foreground">
        <span className="flex items-center gap-2 text-[14px] opacity-80">
          <CrownIcon className="size-4 text-gold" aria-hidden />
          {club?.active ? "Вы в клубе" : "Закрытый клуб"}
        </span>
        {club ? (
          <span className="text-[22px] leading-snug font-medium">
            {club.active && club.until ? `до ${formatDate(club.until.slice(0, 10))}` : texts.clubPitch}
          </span>
        ) : (
          <Skeleton className="h-7 w-40 bg-hero-foreground/15" />
        )}
        <span className="text-[13px] opacity-70">{texts.clubChats}</span>
      </Card>

      {club && !club.active && <PromoForm />}

      <Panel icon={BookOpenIcon} title="Библиотека">
        {!guides ? (
          <Skeleton className="h-28 w-full rounded-2xl" />
        ) : (
          <ul className="flex flex-col gap-3">
            {guides.map((g) => (
              <li key={g.slug}>
                <button type="button" onClick={() => setOpen(g.slug)} className="flex w-full flex-col gap-1 rounded-2xl bg-muted/40 p-3 text-left">
                  <span className="flex items-center justify-between gap-2">
                    <span className="text-[15px] font-medium">{g.title}</span>
                    {club?.active ? <Pill>{g.topic}</Pill> : <LockIcon className="size-4 shrink-0 text-muted-foreground" aria-label="В клубе" />}
                  </span>
                  <span className="line-clamp-2 text-[13px] text-muted-foreground">{g.preview}</span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <Sheet open={open !== null} onOpenChange={(o) => !o && setOpen(null)} title="Гайд" description="Материал библиотеки">
        {open && <GuideView slug={open} />}
      </Sheet>
    </>
  );
}

function PromoForm() {
  const redeem = useRedeemPromo();
  const [code, setCode] = useState("");
  function onSubmit(event: FormEvent) {
    event.preventDefault();
    redeem.mutate(code.trim(), {
      onSuccess: () => {
        haptic.success();
        toast.success("Клуб открыт");
      },
      onError: (err) => {
        haptic.error();
        toast.error(err.message === "promo code is already used" ? "Этот код вы уже использовали" : "Такого кода нет, или он закончился");
      },
    });
  }
  return (
    <Card className="p-4">
      <form onSubmit={onSubmit} noValidate className="flex gap-2">
        <Input aria-label="Промокод" placeholder="Промокод" maxLength={64} value={code} onChange={(e) => setCode(e.target.value)} className="h-11 min-w-0 flex-1 rounded-full px-4 text-[15px]" />
        <PillButton type="submit" disabled={redeem.isPending || !code.trim()}>
          Применить
        </PillButton>
      </form>
    </Card>
  );
}

function GuideView({ slug }: { slug: string }) {
  const guide = useGuide(slug).data;
  if (!guide) return <Skeleton className="h-48 w-full rounded-2xl" />;
  return (
    <article className="flex flex-col gap-3 pb-4">
      <h2 className="text-[22px] font-medium tracking-tight">{guide.title}</h2>
      <Markdown text={guide.body} />
      {guide.locked && (
        <p className="flex items-center gap-2 rounded-2xl bg-soft p-3 text-[14px] text-soft-foreground">
          <LockIcon className="size-4 shrink-0" aria-hidden />
          Целиком — в клубе.
        </p>
      )}
    </article>
  );
}

/** Bold text inside a line. */
function inline(text: string): ReactNode[] {
  return text.split(/\*\*(.+?)\*\*/g).map((part, i) => (i % 2 ? <strong key={i}>{part}</strong> : part));
}

/** The little Markdown the guides use: headings, lists, bold, paragraphs. */
function Markdown({ text }: { text: string }) {
  return (
    <>
      {text.split(/\n{2,}/).map((block, i) => {
        if (block.startsWith("## ")) {
          return (
            <h3 key={i} className="mt-2 text-[17px] font-medium">
              {block.slice(3)}
            </h3>
          );
        }
        if (block.startsWith("- ")) {
          return (
            <ul key={i} className="list-disc space-y-1 pl-5 text-[15px] leading-relaxed text-muted-foreground">
              {block.split("\n").map((line) => (
                <li key={line}>{inline(line.slice(2))}</li>
              ))}
            </ul>
          );
        }
        return (
          <p key={i} className="text-[15px] leading-relaxed text-muted-foreground">
            {inline(block)}
          </p>
        );
      })}
    </>
  );
}
