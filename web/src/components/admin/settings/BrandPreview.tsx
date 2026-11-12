// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties, ReactNode } from "react";
import { useT } from "../../../lib/i18n";
import { contrast, type ResolvedBrand } from "../../../lib/brand";
import { useModeOrLight } from "../../../theme/theme";

interface Props {
  // brand is the brand as it would be with the unsaved values: the name, and the colours the interface would use.
  brand: ResolvedBrand;
  // logo and icon are the pictures in use (the server's), or null.
  logo: string | null;
  icon: string | null;
}

const figure: CSSProperties = { margin: 0, minWidth: 0 };
const caption: CSSProperties = { margin: "0 0 6px", fontSize: 12, fontWeight: 600, color: "var(--ant-color-text-secondary)" };

function Item({ children, style, current }: { children: ReactNode; style?: CSSProperties; current?: boolean }) {
  return (
    <div aria-current={current ? "page" : undefined} style={{ padding: "4px 8px", borderRadius: 4, fontSize: 12, ...style }}>
      {children}
    </div>
  );
}

// BrandPreview draws the unsaved brand in three small scenes: the sidebar, the sign-in page and the icons. It reads the values it is
// given and nothing else changes: the page's own brand and theme stay as they are until Save. Its colours are set as style values,
// which the page's policy allows.
export default function BrandPreview({ brand, logo, icon }: Props) {
  const { t } = useT();
  const mode = useModeOrLight();
  const palette = mode === "dark" ? brand.dark : brand.light;
  const side = brand.sidebar;
  // The wordmark has white letters, which read only on a dark sidebar; on a light one the tile logo is drawn instead.
  const wordmarkReads = contrast("#FFFFFF", side.bg) >= 3;
  const sideFigure =
    logo !== null ? (
      <img src={logo} alt={brand.name} height={22} style={{ maxWidth: "100%" }} />
    ) : wordmarkReads ? (
      <img src="/brand/wordmark.svg" alt={brand.name} height={22} />
    ) : (
      <img src="/brand/logo" alt={brand.name} height={22} />
    );
  const tile = (size: number, radius: number | string, mark: number) =>
    icon !== null ? (
      <img src={icon} alt="" width={size} height={size} style={{ borderRadius: radius, objectFit: "cover", background: "#fff" }} />
    ) : (
      <span data-preview-tile style={{ width: size, height: size, borderRadius: radius, background: brand.accent, display: "grid", placeItems: "center" }}>
        <span
          aria-hidden="true"
          style={{ width: mark, height: mark, background: "#FFFFFF", WebkitMask: "url(/brand/mark.svg) center / contain no-repeat", mask: "url(/brand/mark.svg) center / contain no-repeat" }}
        />
      </span>
    );
  const bar: CSSProperties = { height: 8, borderRadius: 4, border: "1px solid var(--ant-color-border-secondary)" };
  return (
    <div role="group" aria-label={t("settings.preview")} style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))", gap: 16, padding: "14px 0", borderBlockStart: "1px solid var(--ant-color-split)" }}>
      <figure style={figure}>
        <figcaption style={caption}>{t("settings.preview.sidebar")}</figcaption>
        <div data-preview="sidebar" style={{ background: side.bg, color: side.text, borderRadius: 6, padding: 8, display: "flex", flexDirection: "column", gap: 4 }}>
          <div style={{ height: 26, display: "flex", alignItems: "center" }}>{sideFigure}</div>
          <Item style={{ background: "rgba(255,255,255,0.10)" }}>{t("nav.files")}</Item>
          <Item current style={{ background: side.active, color: side.onActive, fontWeight: 600 }}>
            {t("nav.settings")}
          </Item>
          <Item>{t("nav.admins")}</Item>
        </div>
      </figure>
      <figure style={figure}>
        <figcaption style={caption}>{t("settings.preview.signin")}</figcaption>
        <div data-preview="signin" style={{ background: "var(--ant-color-bg-layout)", borderRadius: 6, padding: 12, display: "grid", placeItems: "center" }}>
          <div style={{ background: "var(--ant-color-bg-container)", border: "1px solid var(--ant-color-border-secondary)", borderRadius: 6, padding: 12, width: "100%", maxWidth: 160, display: "grid", gap: 6, justifyItems: "center" }}>
            {logo === null ? (
              <img src="/brand/logo" alt={brand.name} height={30} />
            ) : (
              <>
                <img src={logo} alt="" height={26} style={{ maxWidth: "90%" }} />
                <bdi style={{ fontSize: 12, fontWeight: 700 }}>{brand.name}</bdi>
              </>
            )}
            <div style={{ ...bar, width: "100%" }} />
            <div style={{ ...bar, width: "100%" }} />
            <div data-preview-button style={{ ...bar, width: "100%", background: palette.accentFill, border: "none", height: 14 }} />
          </div>
        </div>
      </figure>
      <figure style={figure}>
        <figcaption style={caption}>{t("settings.preview.icons")}</figcaption>
        <div data-preview="icons" style={{ display: "flex", gap: 12, alignItems: "flex-end" }}>
          {[
            [32, 7, 20, "tab"],
            [56, 12, 34, "app"],
            [56, "50%", 30, "round"],
          ].map(([size, radius, mark, name]) => (
            <figure key={name as string} style={{ ...figure, display: "grid", justifyItems: "center", gap: 2 }}>
              {tile(size as number, radius, mark as number)}
              <figcaption style={{ fontSize: 11, color: "var(--ant-color-text-secondary)" }}>{t(`settings.preview.${name as string}`)}</figcaption>
            </figure>
          ))}
        </div>
      </figure>
    </div>
  );
}
