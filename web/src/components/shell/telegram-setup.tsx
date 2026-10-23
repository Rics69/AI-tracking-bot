"use client";

import { useEffect } from "react";

import { webApp } from "@/lib/telegram";

// The page colours of globals.css (--background), so Telegram's header and
// the area around the app blend into it.
const pageColor = { light: "#f1f0ec", dark: "#0b0b0b" } as const;

/**
 * Prepares the page for Telegram: full height, no swipe-to-close while
 * scrolling, light or dark as in the user's Telegram. Outside Telegram it
 * follows the system preference.
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
    const applyTheme = () => {
      const scheme = app.colorScheme === "dark" ? "dark" : "light";
      root.classList.toggle("dark", scheme === "dark");
      if (app.isVersionAtLeast("6.1")) {
        app.setHeaderColor?.(pageColor[scheme]);
        app.setBackgroundColor?.(pageColor[scheme]);
      }
      if (app.isVersionAtLeast("7.10")) app.setBottomBarColor?.(pageColor[scheme]);
    };
    applyTheme();
    app.onEvent("themeChanged", applyTheme);

    app.ready();
    app.expand();
    if (app.isVersionAtLeast("7.7")) app.disableVerticalSwipes?.();
    return () => app.offEvent("themeChanged", applyTheme);
  }, []);
  return null;
}
