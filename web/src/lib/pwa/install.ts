// SPDX-License-Identifier: AGPL-3.0-or-later

// Installing the web app (decision 65). Browsers that can install fire beforeinstallprompt; the menu's entry is shown only
// then. iPhone and iPad Safari have no prompt, so they get instructions instead. The phone's card (decision 179) offers the
// same install on its own, and keeps the person's choice under one browser key.

import { writable } from "../store";
import { inFrame } from "../frame";

interface InstallPromptEvent extends Event {
  prompt(): Promise<{ outcome: "accepted" | "dismissed" }>;
}

let deferred: InstallPromptEvent | null = null;

// canInstall is true while the browser has offered an install that has not been used or refused yet.
export const canInstall = writable(false);

// listenForInstall keeps the offer. It must run before the page is drawn, or an early offer is missed. A frame keeps
// no offer (decision 214): the drive is installed from its own tab, not from inside a host application.
export function listenForInstall(): void {
  if (inFrame()) return;
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

// INSTALL_PROMPT_KEY is the one key the install card's choice is kept under, per browser and not per person: the app is
// installed for the device, so the next person who signs in on it is not asked again either.
export const INSTALL_PROMPT_KEY = "selfcloud.installPrompt";

// INSTALL_DISMISS_MS is how long "Not now" and the close icon hide the card: fourteen days.
export const INSTALL_DISMISS_MS = 14 * 24 * 60 * 60 * 1000;

// installDismissed reads the choice. A storage that throws, is empty, or holds something unreadable is not a dismissal:
// the card must never break the page, and a person whose storage is refused is offered the install as a new visitor is.
export function installDismissed(now: number = Date.now()): boolean {
  try {
    const raw = window.localStorage.getItem(INSTALL_PROMPT_KEY);
    if (!raw) return false;
    const choice = JSON.parse(raw) as { at?: unknown; forever?: unknown };
    if (choice.forever === true) return true;
    if (typeof choice.at !== "number" || !Number.isFinite(choice.at)) return false;
    return now - choice.at < INSTALL_DISMISS_MS;
  } catch {
    return false;
  }
}

// dismissInstallPrompt keeps the choice: the time for "Not now" and the close icon, forever for "Don't remind me again".
// A write the storage refuses is swallowed; the card still hides for this page load.
export function dismissInstallPrompt(forever: boolean): void {
  try {
    window.localStorage.setItem(INSTALL_PROMPT_KEY, JSON.stringify({ at: Date.now(), forever }));
  } catch {
    // The choice cannot be kept; nothing else changes.
  }
}

// InstallOfferInput is every condition the card's gate reads. The booleans are passed in so the gate is one function the
// tests can ask with each condition on its own; now is the clock the dismissal is measured against.
export interface InstallOfferInput {
  signedIn: boolean;
  inShell: boolean;
  impersonating: boolean;
  coarsePointer: boolean;
  canInstall: boolean;
  ios: boolean;
  now?: number;
}

// shouldOfferInstall says whether the phone's card may be offered: a signed-in person in the shell (not a share link, the
// sign-in or the forced-password page), not viewing as another person, not in the installed app, on a finger, with either
// the browser's offer or iPhone/iPad Safari, and not dismissed.
export function shouldOfferInstall(input: InstallOfferInput): boolean {
  // A frame is never offered the install (decision 214), whatever the other conditions say.
  if (inFrame()) return false;
  if (!input.signedIn || !input.inShell || input.impersonating || !input.coarsePointer) return false;
  if (isStandalone()) return false;
  if (!input.canInstall && !input.ios) return false;
  return !installDismissed(input.now);
}

// offeredThisLoad marks the one card a page load shows. It is not kept in storage: a reload is a new page load, and the
// dismissal there is the person's own choice, not this flag.
let offeredThisLoad = false;

// takeInstallOffer gives the page load's one offer to its first caller; every later caller gets false.
export function takeInstallOffer(): boolean {
  if (offeredThisLoad) return false;
  offeredThisLoad = true;
  return true;
}

// resetInstallOffer gives the offer back. Only tests call it, between their cases; a page never does.
export function resetInstallOffer(): void {
  offeredThisLoad = false;
}
