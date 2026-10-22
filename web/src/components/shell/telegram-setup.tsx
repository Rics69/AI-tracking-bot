"use client";

import { useEffect } from "react";

import { webApp } from "@/lib/telegram";

/**
 * Prepares the page for Telegram: full height, no swipe-to-close while
 * scrolling, header and background in the theme's colours. Outside Telegram
 * it only follows the system light/dark preference.
 */
export function TelegramSetup() {
  useEffect(() => {
    const root = document.documentElement;
    const app = webApp();
    if (!app) {
      const dark = window.matchMedia("(prefers-color-scheme: dark)");
      const apply = () => root.classList.toggle("dark", dark.matches);
      apply();
      dark.addEventListener("change", apply);
      return () => dark.removeEventListener("change", apply);
    }

    root.classList.add("tg");
    const applyTheme = () => root.classList.toggle("dark", app.colorScheme === "dark");
    applyTheme();
    app.onEvent("themeChanged", applyTheme);

    app.ready();
    app.expand();
    if (app.isVersionAtLeast("7.7")) app.disableVerticalSwipes?.();
    if (app.isVersionAtLeast("6.1")) {
      app.setHeaderColor?.("secondary_bg_color");
      app.setBackgroundColor?.("secondary_bg_color");
    }
    if (app.isVersionAtLeast("7.10")) app.setBottomBarColor?.("secondary_bg_color");
    return () => app.offEvent("themeChanged", applyTheme);
  }, []);
  return null;
}
