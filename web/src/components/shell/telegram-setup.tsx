"use client";

import { useEffect } from "react";

import { webApp } from "@/lib/telegram";

// The page colour of globals.css (--background), so Telegram's header and the
// area around the app blend into it.
const pageColor = "#08080c";

/**
 * Prepares the page for Telegram: full height, no swipe-to-close while
 * scrolling. The app is always dark, whatever the theme of Telegram or the
 * system.
 */
export function TelegramSetup() {
  useEffect(() => {
    const app = webApp();
    if (!app) return;
    document.documentElement.classList.add("tg");
    if (app.isVersionAtLeast("6.1")) {
      app.setHeaderColor?.(pageColor);
      app.setBackgroundColor?.(pageColor);
    }
    if (app.isVersionAtLeast("7.10")) app.setBottomBarColor?.(pageColor);
    app.ready();
    app.expand();
    if (app.isVersionAtLeast("7.7")) app.disableVerticalSwipes?.();
  }, []);
  return null;
}
