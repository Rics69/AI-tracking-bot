"use client";

// A thin typed layer over telegram-web-app.js (loaded in the root layout).
// Outside Telegram (a plain browser in development) every call is a no-op.
// https://core.telegram.org/bots/webapps

type HapticImpact = "light" | "medium" | "heavy" | "rigid" | "soft";
type HapticNotification = "error" | "success" | "warning";

type BackButton = {
  show(): void;
  hide(): void;
  onClick(cb: () => void): void;
  offClick(cb: () => void): void;
};

export type WebApp = {
  initData: string;
  version: string;
  platform: string;
  colorScheme: "light" | "dark";
  ready(): void;
  expand(): void;
  close(): void;
  isVersionAtLeast(version: string): boolean;
  disableVerticalSwipes?(): void;
  setHeaderColor?(color: string): void;
  setBackgroundColor?(color: string): void;
  setBottomBarColor?(color: string): void;
  onEvent(event: string, cb: () => void): void;
  offEvent(event: string, cb: () => void): void;
  openTelegramLink(url: string): void;
  openLink(url: string): void;
  showConfirm?(message: string, cb: (ok: boolean) => void): void;
  BackButton: BackButton;
  HapticFeedback: {
    impactOccurred(style: HapticImpact): void;
    notificationOccurred(type: HapticNotification): void;
    selectionChanged(): void;
  };
};

/** The Mini App object, or undefined outside Telegram or before the script ran. */
export function webApp(): WebApp | undefined {
  if (typeof window === "undefined") return undefined;
  const app = (window as { Telegram?: { WebApp?: WebApp } }).Telegram?.WebApp;
  // The script also runs in a plain browser; there it has no init data.
  return app && app.initData ? app : undefined;
}

/** True inside the Telegram client. */
export function inTelegram(): boolean {
  return webApp() !== undefined;
}

/** Feedback for taps and results; silent outside Telegram and on old clients. */
export const haptic = {
  tap: () => webApp()?.HapticFeedback.impactOccurred("light"),
  select: () => webApp()?.HapticFeedback.selectionChanged(),
  success: () => webApp()?.HapticFeedback.notificationOccurred("success"),
  error: () => webApp()?.HapticFeedback.notificationOccurred("error"),
};

/** Opens Telegram's share dialog for a link: the natural way to send an invite. */
export function shareLink(url: string, text: string): boolean {
  const app = webApp();
  if (!app) return false;
  const share = new URL("https://t.me/share/url");
  share.searchParams.set("url", url);
  share.searchParams.set("text", text);
  app.openTelegramLink(share.toString());
  return true;
}

/** Closes the Mini App; outside Telegram there is nothing to close. */
export function closeApp() {
  webApp()?.close();
}
