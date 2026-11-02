// SPDX-License-Identifier: AGPL-3.0-or-later

// Installing the web app (decision 65). Browsers that can install fire beforeinstallprompt; the menu's entry is shown only
// then. iPhone and iPad Safari have no prompt, so they get instructions instead.

import { writable } from "../store";

interface InstallPromptEvent extends Event {
  prompt(): Promise<{ outcome: "accepted" | "dismissed" }>;
}

let deferred: InstallPromptEvent | null = null;

// canInstall is true while the browser has offered an install that has not been used or refused yet.
export const canInstall = writable(false);

// listenForInstall keeps the offer. It must run before the page is drawn, or an early offer is missed.
export function listenForInstall(): void {
  window.addEventListener("beforeinstallprompt", (event) => {
    event.preventDefault();
    deferred = event as InstallPromptEvent;
    canInstall.set(true);
  });
  window.addEventListener("appinstalled", () => {
    deferred = null;
    canInstall.set(false);
  });
}

// promptInstall shows the browser's install dialog. An offer is used once.
export async function promptInstall(): Promise<void> {
  const event = deferred;
  if (!event) return;
  deferred = null;
  canInstall.set(false);
  await event.prompt().catch(() => undefined);
}

// isStandalone is true when the app already runs as an installed app, so nothing offers to install it again.
export function isStandalone(): boolean {
  if (typeof window === "undefined") return false;
  const standalone = typeof window.matchMedia === "function" && window.matchMedia("(display-mode: standalone)").matches;
  return standalone || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

// isIOS is true for iPhone, iPod and iPad, including an iPad that reports itself as a Mac with a touch screen.
export function isIOS(): boolean {
  const ua = navigator.userAgent;
  return /iPad|iPhone|iPod/.test(ua) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

// iosDialogOpen says whether the iPhone instructions are open for the person who is signed in now. They are opened for one
// person, and a change of person closes them.
export function iosDialogOpen(openedFor: string | null, who: string | null): boolean {
  return openedFor !== null && who !== null && openedFor === who;
}
