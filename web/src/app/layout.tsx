import type { Metadata, Viewport } from "next";
import Script from "next/script";

import { BottomNav } from "@/components/shell/bottom-nav";
import { Gate } from "@/components/shell/gate";
import { TelegramSetup } from "@/components/shell/telegram-setup";
import { OperationSheetProvider } from "@/components/operations/sheet-context";
import { Toaster } from "@/components/ui/sonner";
import { texts } from "@/content/texts";

import { Providers } from "./providers";
import "./globals.css";

export const metadata: Metadata = {
  title: texts.appName,
  description: "Траты и доходы с Сейвиком",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  maximumScale: 1,
  userScalable: false, // an app, not a page: no accidental zoom on inputs
  viewportFit: "cover",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="ru" className="h-full antialiased" suppressHydrationWarning>
      <body className="min-h-full bg-background font-sans text-foreground">
        <Providers>
          <TelegramSetup />
          <Gate>
            <OperationSheetProvider>
              <main className="mx-auto flex w-full max-w-lg flex-col gap-6 px-4 pt-4 pb-[calc(6rem+var(--safe-bottom))]">
                {children}
              </main>
              <BottomNav />
            </OperationSheetProvider>
          </Gate>
        </Providers>
        <Toaster position="top-center" />
        {/* Telegram asks to load it before anything else: it sets up the theme and init data. */}
        <Script src="https://telegram.org/js/telegram-web-app.js" strategy="beforeInteractive" />
      </body>
    </html>
  );
}
