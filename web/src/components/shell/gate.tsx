"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { toast } from "sonner";

import { ApiError } from "@/api/client";
import { useMe, useUpdateMe } from "@/api/queries";
import { Savik, usePreloadPoses } from "@/components/savik/savik";
import { Button } from "@/components/ui/button";
import { texts } from "@/content/texts";
import { haptic } from "@/lib/telegram";

/**
 * Lets the app in only for a known user who accepted the data policy.
 * Outside Telegram (no init data) the API answers 401: the user is asked to
 * open the app from Telegram instead.
 */
export function Gate({ children }: { children: ReactNode }) {
  const me = useMe();
  usePreloadPoses();
  useDeviceTimezone(me.data?.consented === true && !me.data.timezone);

  if (me.isPending) {
    return (
      <Centered>
        <Savik mood="neutral" phrase={null} size={140} />
      </Centered>
    );
  }
  if (me.error) {
    const outside = me.error instanceof ApiError && me.error.status === 401;
    return (
      <Centered>
        <Savik mood={outside ? "outside" : "error"} size={160} />
        <div className="flex flex-col gap-2 text-center">
          <h1 className="text-xl font-semibold">
            {outside ? texts.outsideTitle : "Не получилось загрузиться"}
          </h1>
          <p className="text-muted-foreground">{outside ? texts.outsideBody : me.error.message}</p>
        </div>
        {!outside && (
          <Button size="lg" className="h-11 px-6" onClick={() => void me.refetch()}>
            Повторить
          </Button>
        )}
      </Centered>
    );
  }
  if (!me.data.consented) return <Consent />;
  return children;
}

function Consent() {
  const update = useUpdateMe();
  return (
    <Centered>
      <Savik mood="welcome" size={168} />
      <section className="flex flex-col gap-3 rounded-2xl bg-card p-5 text-card-foreground">
        <h1 className="text-lg font-semibold">{texts.consentTitle}</h1>
        <p className="text-[15px] leading-relaxed text-muted-foreground">{texts.consentBody}</p>
      </section>
      <Button
        size="lg"
        className="h-12 w-full text-base"
        disabled={update.isPending}
        onClick={() =>
          update.mutate(
            { consent: true },
            {
              onSuccess: () => haptic.success(),
              onError: (err) => toast.error(err.message),
            },
          )
        }
      >
        {texts.consentButton}
      </Button>
    </Centered>
  );
}

/**
 * Sets the timezone of the device once, if the user never chose one: "today"
 * in the bot, the reports and the app then match the user's clock.
 */
function useDeviceTimezone(needed: boolean) {
  const update = useUpdateMe();
  const done = useRef(false);
  useEffect(() => {
    if (!needed || done.current) return;
    done.current = true;
    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    if (tz) update.mutate({ timezone: tz }, { onError: () => undefined }); // not worth bothering the user
  }, [needed, update]);
}

function Centered({ children }: { children: ReactNode }) {
  return (
    <div className="mx-auto flex min-h-dvh w-full max-w-sm flex-col items-center justify-center gap-6 px-6 py-10">
      {children}
    </div>
  );
}
