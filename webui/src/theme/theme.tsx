// SPDX-License-Identifier: AGPL-3.0-or-later

import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useState, type ReactNode } from "react";
import { ConfigProvider, theme as antdTheme } from "antd";
import type { ThemeConfig } from "antd";
import { readPref, writePref, PREF_THEME } from "../lib/prefs";
import { useStore } from "../lib/store";
import { brand, resolveBrand } from "../lib/brand";
import { readNonce } from "../csp/nonce";
import { RADIUS, SIDEBAR, type Palette, type Sidebar } from "./tokens";

// The theme has three parts: the mode (light or dark, from the person's choice or the device), the
// palette of that mode with the installation's brand applied, and the antd token set built from
// both. A choice of "system" follows the device while the page is open.

export type Mode = "light" | "dark";
export type Pref = Mode | "system";

interface ThemeState {
  mode: Mode;
  pref: Pref;
  palette: Palette;
  sidebar: Sidebar;
  setPref: (p: Pref) => void;
  toggle: () => void;
}

const ThemeCtx = createContext<ThemeState | null>(null);

function readInitialPref(): Pref {
  const stored = readPref(PREF_THEME);
  // "light" and "dark" are what earlier versions wrote; they still mean an explicit choice.
  return stored === "light" || stored === "dark" || stored === "system" ? stored : "system";
}

function deviceDark(): boolean {
  return typeof window !== "undefined" && !!window.matchMedia?.("(prefers-color-scheme: dark)").matches;
}

// antdTokens returns the antd theme for a mode, from the palette and sidebar in effect.
export function antdTokens(mode: Mode, p: Palette, sb: Sidebar): ThemeConfig {
  const dark = mode === "dark";
  return {
    algorithm: dark ? antdTheme.darkAlgorithm : antdTheme.defaultAlgorithm,
    token: {
      colorPrimary: p.accent,
      colorInfo: p.accent,
      colorSuccess: p.success,
      colorWarning: p.warning,
      colorError: p.danger,
      colorLink: p.accentText,
      colorLinkHover: p.accentHover,
      colorLinkActive: p.accentActive,
      colorTextBase: p.text,
      colorBgBase: p.card,
      colorText: p.text,
      colorTextHeading: p.text,
      colorTextSecondary: p.muted,
      colorTextTertiary: p.faint,
      colorTextQuaternary: dark ? p.line2 : "#96A6BD",
      colorBgLayout: p.page,
      colorBgContainer: p.card,
      // Menus and popovers sit one step above the dark card, or they would have no edge on it.
      colorBgElevated: dark ? p.line : p.card,
      colorBorder: p.line2,
      colorBorderSecondary: p.line,
      colorSplit: p.line,
      colorPrimaryBg: p.accentSoft,
      colorPrimaryBgHover: p.accentSoftHover,
      colorPrimaryBorder: p.accentBorder,
      colorPrimaryBorderHover: p.accentBorderHover,
      colorPrimaryHover: p.accentHover,
      colorPrimaryActive: p.accentActive,
      colorPrimaryText: p.accentText,
      colorPrimaryTextHover: p.accentHover,
      colorPrimaryTextActive: p.accentActive,
      colorInfoBg: p.accentSoft,
      colorInfoBgHover: p.accentSoftHover,
      colorInfoBorder: p.accentBorder,
      colorInfoText: p.accentText,
      // Pinned, not derived: derived from these seeds, antd's soft steps come out grey-green and grey-rose.
      colorSuccessBg: p.successSoft,
      colorSuccessBgHover: p.successBorder,
      colorSuccessBorder: p.successBorder,
      colorSuccessText: p.successText,
      colorWarningBg: p.warningSoft,
      colorWarningBgHover: p.warningBorder,
      colorWarningBorder: p.warningBorder,
      colorWarningText: p.warningText,
      colorErrorBg: p.dangerSoft,
      colorErrorBgHover: p.dangerBorder,
      colorErrorBorder: p.dangerBorder,
      colorErrorText: p.dangerText,
      ...presetRamps(p),
      // A selected row in a dark popover: shows against the popover ground.
      ...(dark ? { controlItemBgActive: "#31486C", controlItemBgActiveHover: "#31486C" } : {}),
      borderRadius: RADIUS,
      borderRadiusLG: RADIUS,
      borderRadiusSM: RADIUS,
      borderRadiusXS: RADIUS,
    },
    components: {
      // A status Tag writes its label in the bare semantic colour, which is too faint on its ground.
      Tag: {
        colorSuccess: p.successText,
        colorWarning: p.warningText,
        colorError: p.dangerText,
        colorInfo: p.accentText,
      },
      Button: {
        fontWeight: 600,
        primaryShadow: "none",
        defaultShadow: "none",
        dangerShadow: "none",
        // White on the dark accent glyph colour is 3:1, so a solid primary takes the dark fill.
        // Pinned value by value: `algorithm: true` would re-derive every Button token.
        ...(dark
          ? {
              colorPrimary: p.accentFill,
              colorPrimaryHover: p.accentFillHover,
              colorPrimaryActive: p.accentFillActive,
              primaryColor: "#FFFFFF",
              defaultHoverColor: p.accentText,
              defaultHoverBorderColor: p.accentText,
              defaultActiveColor: p.accent,
              defaultActiveBorderColor: p.accent,
            }
          : {}),
      },
      Menu: {
        itemBorderRadius: RADIUS,
        subMenuItemBorderRadius: RADIUS,
        groupTitleFontSize: 11.5,
        ...(dark ? { itemSelectedColor: p.accentText } : {}),
        // `theme="dark"` is the sidebar, blue in both modes.
        darkItemBg: sb.bg,
        darkSubMenuItemBg: sb.bg,
        darkPopupBg: sb.bg,
        darkItemColor: sb.text,
        darkItemHoverBg: SIDEBAR.hover,
        darkItemHoverColor: sb.text,
        darkItemSelectedBg: sb.active,
        darkItemSelectedColor: sb.onActive,
        darkGroupTitleColor: sb.caption,
      },
    },
  };
}

// Preset tag colours (`<Tag color="blue">`) paint from antd's own ramps, so each preset is mapped to
// the nearest palette family. Purple goes to the accent; cyan to teal.
const PRESET_FAMILY = {
  blue: "accent", geekblue: "accent", purple: "accent",
  cyan: "teal",
  green: "success", lime: "success",
  red: "danger", volcano: "danger", magenta: "danger", pink: "danger",
  orange: "warning", gold: "warning", yellow: "warning",
} as const;

function presetRamps(p: Palette): Record<string, string> {
  const family = {
    accent: [p.accentSoft, p.accentBorder, p.accentFill, p.accentText],
    teal: [p.tealSoft, p.tealBorder, p.teal, p.tealText],
    success: [p.successSoft, p.successBorder, p.success, p.successText],
    warning: [p.warningSoft, p.warningBorder, p.warning, p.warningText],
    danger: [p.dangerSoft, p.dangerBorder, p.danger, p.dangerText],
  };
  const out: Record<string, string> = {};
  for (const [name, fam] of Object.entries(PRESET_FAMILY)) {
    const [soft, border, solid, text] = family[fam];
    out[`${name}1`] = soft;
    out[`${name}3`] = border;
    out[`${name}6`] = solid;
    out[`${name}7`] = text;
  }
  return out;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [pref, setPrefState] = useState<Pref>(readInitialPref);
  const [dark, setDark] = useState<boolean>(deviceDark);
  const current = useStore(brand);
  const resolved = useMemo(() => resolveBrand(current), [current]);

  // Following the device means following it while the page is open.
  useEffect(() => {
    const mq = window.matchMedia?.("(prefers-color-scheme: dark)");
    if (!mq) return;
    const onChange = (e: MediaQueryListEvent) => setDark(e.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, []);

  const mode: Mode = pref === "system" ? (dark ? "dark" : "light") : pref;
  const palette = mode === "dark" ? resolved.dark : resolved.light;
  const sidebar = resolved.sidebar;

  useEffect(() => {
    document.documentElement.dataset.theme = mode;
  }, [mode]);

  const setPref = useCallback((p: Pref) => {
    writePref(PREF_THEME, p);
    setPrefState(p);
  }, []);

  // The top bar's one-click switch is an explicit choice, so it leaves "system" behind.
  const toggle = useCallback(() => setPref(mode === "dark" ? "light" : "dark"), [mode, setPref]);

  const antd = useMemo(() => antdTokens(mode, palette, sidebar), [mode, palette, sidebar]);
  const linkColour = antd.token?.colorLink;

  // The plain links of the page (the stylesheet's a rule) take the antd link token, so they match the antd links in both modes.
  useLayoutEffect(() => {
    if (typeof linkColour === "string") document.documentElement.style.setProperty("--link-colour", linkColour);
  }, [linkColour]);
  const state = useMemo<ThemeState>(
    () => ({ mode, pref, palette, sidebar, setPref, toggle }),
    [mode, pref, palette, sidebar, setPref, toggle],
  );

  return (
    <ThemeCtx.Provider value={state}>
      {/* The nonce is read from the page's meta tag; antd writes its own styles with it. */}
      <ConfigProvider theme={antd} csp={{ nonce: readNonce() }}>
        {children}
      </ConfigProvider>
    </ThemeCtx.Provider>
  );
}

export function useTheme(): ThemeState {
  const v = useContext(ThemeCtx);
  if (!v) throw new Error("useTheme outside ThemeProvider");
  return v;
}

/** The palette of the mode being painted, with the brand applied. */
export function usePalette(): Palette {
  return useTheme().palette;
}

/** The sidebar colours, with the brand applied. */
export function useSidebar(): Sidebar {
  return useTheme().sidebar;
}

// Accent colours for glyphs, from the palette.
export type Accent = "blue" | "teal" | "green" | "red" | "amber" | "grey";

export function useAccent(): Record<Accent, string> {
  const p = usePalette();
  return {
    blue: p.accent,
    teal: p.teal,
    green: p.success,
    amber: p.warning,
    red: p.danger,
    grey: p.faint,
  };
}
