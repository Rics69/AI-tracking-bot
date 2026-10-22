"use client";

import { AnimatePresence, motion, useReducedMotion } from "motion/react";
import Image from "next/image";
import { useEffect, useSyncExternalStore } from "react";

import { cn } from "@/lib/utils";
import { phraseOf, poses, type Mood } from "@/content/savik";

type Props = {
  mood: Mood;
  /** Pixels; the art is square. */
  size?: number;
  /** What Savik says; by default a random phrase of the mood. Null hides the bubble. */
  phrase?: string | null;
  className?: string;
};

/**
 * Savik, the mascot. The poses are still pictures, the life is in motion:
 * he slides in, breathes while idle, and swaps poses when the mood changes.
 * With reduced motion he just stands there.
 */
export function Savik({ mood, size = 160, phrase, className }: Props) {
  const reduce = useReducedMotion();
  const pose = poses[mood];

  return (
    <div className={cn("flex flex-col items-center gap-3", className)}>
      <div className="relative" style={{ width: size, height: size }}>
        <AnimatePresence mode="wait" initial={!reduce}>
          <motion.div
            key={pose}
            className="absolute inset-0"
            initial={reduce ? false : { y: 32, opacity: 0, scale: 0.92 }}
            animate={{ y: 0, opacity: 1, scale: 1 }}
            exit={reduce ? undefined : { y: 16, opacity: 0, scale: 0.96 }}
            transition={{ type: "spring", stiffness: 260, damping: 20 }}
          >
            <motion.div
              className="size-full"
              // Breathing: a slow, barely visible rise and swell.
              animate={reduce ? undefined : { y: [0, -3, 0], scale: [1, 1.02, 1] }}
              transition={{ duration: 3.2, repeat: Infinity, ease: "easeInOut", delay: 0.6 }}
            >
              <Image
                src={`/savik/512/${pose}.webp`}
                alt=""
                width={size}
                height={size}
                priority
                unoptimized
                className="size-full select-none"
                draggable={false}
              />
            </motion.div>
          </motion.div>
        </AnimatePresence>
      </div>
      {phrase !== null && <Bubble text={phrase} mood={mood} reduce={!!reduce} />}
    </div>
  );
}

// The random phrase of each mood, picked once per page session in the browser.
const picked = new Map<Mood, string>();
const noSubscribe = () => () => {};

/**
 * A random phrase of the mood. Picked only in the browser: the server
 * prerender has no phrase (Math.random() there would disagree with the client).
 */
function useRandomPhrase(mood: Mood): string | undefined {
  return useSyncExternalStore(
    noSubscribe,
    () => {
      if (!picked.has(mood)) picked.set(mood, phraseOf(mood));
      return picked.get(mood);
    },
    () => undefined,
  );
}

/** The speech bubble; without text it says a random phrase of the mood. */
function Bubble({ text, mood, reduce }: { text?: string; mood: Mood; reduce: boolean }) {
  const random = useRandomPhrase(mood);
  const shown = text ?? random;
  return (
    <AnimatePresence mode="wait">
      {shown && (
        <motion.p
          key={shown}
          role="status"
          className="relative max-w-72 rounded-2xl bg-card px-4 py-3 text-center text-[15px] leading-snug text-card-foreground shadow-sm"
          initial={reduce ? false : { opacity: 0, y: 6, scale: 0.97 }}
          animate={{ opacity: 1, y: 0, scale: 1 }}
          exit={reduce ? undefined : { opacity: 0, y: -4 }}
          transition={{ delay: reduce ? 0 : 0.35, duration: 0.25 }}
        >
          {shown}
        </motion.p>
      )}
    </AnimatePresence>
  );
}

/** Warms the browser cache with every pose of the moods, so a mood swap does not flicker. */
export function usePreloadPoses() {
  useEffect(() => {
    const timer = setTimeout(() => {
      for (const pose of new Set(Object.values(poses))) {
        const img = new window.Image();
        img.src = `/savik/512/${pose}.webp`;
      }
    }, 1500);
    return () => clearTimeout(timer);
  }, []);
}
