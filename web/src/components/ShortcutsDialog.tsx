// SPDX-License-Identifier: AGPL-3.0-or-later

import { Fragment, useEffect, useRef, type CSSProperties, type ReactNode } from "react";
import { Modal, Skeleton, theme as antdTheme } from "antd";
import { useT } from "../lib/i18n";
import { isMacPlatform } from "../lib/files/shortcutHint";
import { SHORTCUT_GROUPS, type ShortcutRow } from "../lib/shortcuts";
import { useShortcutsText } from "../lib/shortcutsText";
import DriveTextFailure from "./drive/DriveTextFailure";

const MAC_KEYS = typeof navigator !== "undefined" && isMacPlatform(navigator);

const ARROWS = new Set(["↑", "↓", "←", "→"]);

// cap is the name a key cap shows. The names are the keyboard's own, not translated; Apple's systems show their signs
// (the command sign for Ctrl, the option sign for Alt, the delete sign for Backspace). The menu key is named Menu.
function cap(name: string): string {
  if (name === "ContextMenu") return "Menu";
  if (MAC_KEYS) {
    if (name === "Ctrl" || name === "Meta") return "⌘";
    if (name === "Alt") return "⌥";
    if (name === "Backspace") return "⌫";
  }
  return name;
}

// Chord draws one way of pressing a row's keys: together, with a plus between them. A run of arrows is one set (any of
// them moves), so its caps sit side by side without a plus. The caps keep their Latin order on a right-to-left page.
function Chord({ parts }: { parts: string[] }) {
  const nodes: ReactNode[] = [];
  parts.forEach((p, i) => {
    if (i > 0) {
      nodes.push(
        ARROWS.has(p) && ARROWS.has(parts[i - 1]) ? (
          <span key={`g${i}`} className="shortcuts-gap" />
        ) : (
          <span key={`p${i}`} className="shortcuts-plus">
            +
          </span>
        ),
      );
    }
    nodes.push(
      <kbd key={`k${i}`} className="shortcuts-kbd">
        {cap(p)}
      </kbd>,
    );
  });
  return (
    <span className="shortcuts-chord" dir="ltr">
      {nodes}
    </span>
  );
}

// Keys draws a row's keys: its ways one after another with "or" between them, and a chord's letter after "then".
function Keys({ row }: { row: ShortcutRow }) {
  const { t } = useT();
  return (
    <span className="shortcuts-keys">
      {row.keys.map((alt, i) => (
        <Fragment key={i}>
          {i > 0 && <span className="shortcuts-or">{t("shortcuts.or")}</span>}
          <Chord parts={alt} />
        </Fragment>
      ))}
      {row.then && (
        <>
          <span className="shortcuts-then">{t("shortcuts.then")}</span>
          <span className="shortcuts-chord" dir="ltr">
            {row.then.map((k, i) => (
              <kbd key={i} className="shortcuts-kbd">
                {cap(k)}
              </kbd>
            ))}
          </span>
        </>
      )}
    </span>
  );
}

// ShortcutsDialog is the keyboard shortcuts list (ks1, decision 174): a lazy chunk, mounted only while it is open, as the
// sidebar's New folder dialog is. It names only the shortcuts the code answers (lib/shortcuts.ts), grouped, each row with
// its keys as caps. The strings are a catalogue of their own, loaded with the dialog (lib/shortcutsText.ts). The focus
// returns to where it was before the list opened: the dialog unmounts on close, so the return is made here rather than
// left to antd's after the closing motion.
export default function ShortcutsDialog({ onClose }: { onClose: () => void }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const { ready, failed, retry } = useShortcutsText();
  const back = useRef<HTMLElement | null>(null);
  useEffect(() => {
    back.current = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    return () => back.current?.focus({ preventScroll: true });
  }, []);
  return (
    <Modal open title={t("shell.shortcuts")} footer={null} maskClosable={false} onCancel={onClose} width={680}>
      {failed ? (
        <DriveTextFailure retry={retry} />
      ) : !ready ? (
        <Skeleton active title={false} paragraph={{ rows: 8 }} />
      ) : (
        <div
          className="shortcuts-list"
          style={
            {
              "--sc-kbd-bg": token.colorFillQuaternary,
              "--sc-kbd-border": token.colorBorder,
              "--sc-kbd-text": token.colorText,
              "--sc-kbd-note": token.colorTextTertiary,
              "--sc-kbd-radius": `${token.borderRadiusSM}px`,
              "--sc-kbd-font": `${token.fontSizeSM}px`,
            } as CSSProperties
          }
        >
          {SHORTCUT_GROUPS.map((g) => (
            <section key={g.title} className="shortcuts-group">
              <h3 className="shortcuts-heading">
                {t(g.title)}
                {g.note && <span className="shortcuts-note">{t(g.note)}</span>}
              </h3>
              <ul className="shortcuts-rows">
                {g.rows
                  .filter((r) => !r.macOnly || MAC_KEYS)
                  .map((r) => (
                    <li key={`${r.label}:${JSON.stringify(r.keys)}`} className="shortcuts-row">
                      <span className="shortcuts-label">{t(r.label)}</span>
                      <Keys row={r} />
                    </li>
                  ))}
              </ul>
            </section>
          ))}
        </div>
      )}
    </Modal>
  );
}
