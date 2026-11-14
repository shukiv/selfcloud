// SPDX-License-Identifier: AGPL-3.0-or-later

// The phone's offer to install the app (decision 179). Two seconds after the shell is drawn, a signed-in person on a finger
// is asked once: the browser's own install dialog where it made an offer, the iPhone/iPad instructions where Safari has none.
// It is a labelled region at the bottom of the screen, not a modal: it takes no focus and blocks nothing, and Escape while
// the focus is inside it answers Not now. "Not now" and the close icon hide it for fourteen days, "Don't remind me again"
// for good, under one browser key (lib/pwa/install.ts). Its words are a lazy catalogue, loaded only when it is about to show
// (lib/pwa/text.ts), so the main catalogues do not grow.

import { lazy, Suspense, useEffect, useState } from "react";
import { Button, Card, theme as antdTheme } from "antd";
import { X } from "lucide-react";
import IconButton from "./IconButton";
import { currentLang, useLang, useT } from "../lib/i18n";
import { get, useStore } from "../lib/store";
import { session } from "../lib/session";
import { brand, DEFAULT_BRAND } from "../lib/brand";
import { useCoarsePointer } from "../lib/pointer";
import { canInstall, dismissInstallPrompt, isIOS, promptInstall, shouldOfferInstall, takeInstallOffer } from "../lib/pwa/install";
import { loadInstallText } from "../lib/pwa/text";
import { RADIUS } from "../theme/tokens";

const IosInstallDialog = lazy(() => import("./pwa/IosInstallDialog"));

// DELAY_MS is the wait after the shell is drawn; POLL_MS is how often the card checks again while a dialog or a drawer is
// open, so it appears once the last of them closes. MEASURE_MS is how often it re-reads the bars it must sit above.
const DELAY_MS = 2000;
const POLL_MS = 500;
const MEASURE_MS = 1000;

// overlayOpen says whether a dialog or a drawer is on screen. antd keeps a closed modal's wrap in the DOM with display none,
// so only a displayed one counts; the media viewer is the shell's own full-window dialog.
function overlayOpen(): boolean {
  for (const wrap of document.querySelectorAll(".ant-modal-wrap")) {
    if (getComputedStyle(wrap).display !== "none") return true;
  }
  if (document.querySelector(".ant-drawer-open")) return true;
  return !!document.querySelector('[data-testid="preview"]');
}

export default function InstallPrompt() {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  const coarse = useCoarsePointer();
  const me = useStore(session);
  const can = useStore(canInstall);
  const brandName = useStore(brand).name;
  const ios = isIOS();
  const signedIn = me !== null;
  const impersonating = !!me?.impersonation;
  // visible: the words are loaded and the card is drawn. gone: dismissed or installed, so nothing brings it back this load.
  const [visible, setVisible] = useState(false);
  const [gone, setGone] = useState(false);
  const [iosOpen, setIosOpen] = useState(false);
  // lift is the height of the bars the card sits above (the mini player, the update offer), so they stay visible under it.
  const [lift, setLift] = useState(0);

  useEffect(() => {
    if (gone || visible || !coarse || !signedIn || impersonating) return;
    // No offer can come on this device: a mouse, or a finger with neither the browser's offer nor iOS. Nothing is timed.
    if (!can && !ios) return;
    let interval = 0;
    const tryOffer = () => {
      if (!shouldOfferInstall({ signedIn, inShell: true, impersonating, coarsePointer: coarse, canInstall: get(canInstall), ios })) return;
      if (overlayOpen()) return;
      window.clearInterval(interval);
      if (!takeInstallOffer()) return;
      // A catalogue that will not load leaves the card unshown: the page must never break for the offer.
      void loadInstallText(currentLang()).then(
        () => setVisible(true),
        () => undefined,
      );
    };
    const timer = window.setTimeout(() => {
      tryOffer();
      interval = window.setInterval(tryOffer, POLL_MS);
    }, DELAY_MS);
    return () => {
      window.clearTimeout(timer);
      window.clearInterval(interval);
    };
  }, [coarse, signedIn, impersonating, gone, visible, can, ios]);

  // An installed app takes the card away at once, whether the install came from this card or the menu's entry.
  useEffect(() => {
    const installed = () => setGone(true);
    window.addEventListener("appinstalled", installed);
    return () => window.removeEventListener("appinstalled", installed);
  }, []);

  // A language changed while the card is up reads its words in the new language too.
  useEffect(() => {
    if (visible) void loadInstallText(lang).catch(() => undefined);
  }, [visible, lang]);

  useEffect(() => {
    if (!visible) return;
    const measure = () => {
      let height = 0;
      const mini = document.querySelector('[data-testid="media-mini"]');
      if (mini) height += mini.getBoundingClientRect().height + 8;
      const update = document.querySelector('[data-testid="update-prompt"]');
      if (update) height += update.getBoundingClientRect().height + 8;
      setLift(height);
    };
    measure();
    const timer = window.setInterval(measure, MEASURE_MS);
    return () => window.clearInterval(timer);
  }, [visible]);

  if (gone || !visible) return null;

  const name = brandName || DEFAULT_BRAND.name;
  const title = t("pwaPrompt.title", { name });
  // Not now and the close icon keep the time; the link keeps the choice for good. The card hides either way.
  const notNow = () => {
    dismissInstallPrompt(false);
    setGone(true);
  };
  const never = () => {
    dismissInstallPrompt(true);
    setGone(true);
  };
  const install = () => {
    if (ios) {
      setIosOpen(true);
      return;
    }
    // The browser's offer is used once (decision 65): the card goes with it, accepted or not.
    setGone(true);
    void promptInstall();
  };

  return (
    <>
      <Card
        role="region"
        aria-label={title}
        data-testid="install-prompt"
        // Escape answers Not now only while the focus is inside the card: the handler hears the keys of its own buttons.
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            e.stopPropagation();
            notNow();
          }
        }}
        styles={{ body: { padding: 16 } }}
        style={{
          position: "fixed",
          insetInline: 16,
          // Above the safe-area inset, and above the mini player and the update offer when they are shown.
          insetBlockEnd: `calc(16px + env(safe-area-inset-bottom, 0px) + ${lift}px)`,
          maxWidth: 480,
          marginInline: "auto",
          zIndex: 900,
          background: token.colorBgElevated,
          boxShadow: token.boxShadowSecondary,
          borderRadius: RADIUS * 2,
          color: token.colorText,
        }}
      >
        <div style={{ display: "flex", alignItems: "flex-start", gap: 8 }}>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 600, fontSize: token.fontSizeLG }}>{title}</div>
            <div style={{ marginBlockStart: 4, color: token.colorTextSecondary }}>{t("pwaPrompt.line", { name })}</div>
          </div>
          <IconButton label={t("pwa.close")} onClick={notNow} icon={<X size={18} aria-hidden="true" focusable="false" />} style={{ marginBlockStart: -8, marginInlineEnd: -8, flex: "none" }} />
        </div>
        {/* The words stay on the buttons (the owner's icons-first rule keeps words in dialog footers); a finger's targets are 44 px. */}
        <div style={{ display: "flex", gap: 8, marginBlockStart: 12 }}>
          <Button style={{ flex: 1, minHeight: 44 }} onClick={notNow}>
            {t("pwaPrompt.notNow")}
          </Button>
          <Button type="primary" style={{ flex: 1, minHeight: 44 }} onClick={install}>
            {t("pwaPrompt.install")}
          </Button>
        </div>
        <div style={{ marginBlockStart: 8, textAlign: "center" }}>
          <Button type="link" size="small" style={{ minHeight: 32, fontSize: token.fontSizeSM }} onClick={never}>
            {t("pwaPrompt.never")}
          </Button>
        </div>
      </Card>
      {iosOpen && (
        <Suspense fallback={null}>
          <IosInstallDialog onClose={() => setIosOpen(false)} />
        </Suspense>
      )}
    </>
  );
}