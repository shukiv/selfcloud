// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert, Collapse } from "antd";
import { useMemo } from "react";
import { useT } from "../../../lib/i18n";
import { resolveBrand, validAccent, DEFAULT_ACCENT, type Brand, type ResolvedBrand, type SidebarWire, type TokenWire } from "../../../lib/brand";
import type { Picture, PictureKind, Setting, SettingsAnswer } from "../../../lib/settings/api";
import type { FieldSpec, SectionSpec } from "../../../lib/settings/fields";
import type { Draft, Edits } from "../../../lib/settings/form";
import BrandPreview from "./BrandPreview";
import FieldRow from "./FieldRow";
import PictureRow, { pictureSrc } from "./PictureRow";
import { CardFrame, SectionFoot, type Notice } from "./SectionCard";

interface Props {
  section: SectionSpec;
  settings: Setting[];
  pictures: Picture[];
  edits: Edits;
  errors: Record<string, string>;
  notice: Notice | null;
  dirty: boolean;
  busy: boolean;
  wide: boolean;
  onChange: (key: string, value: Draft) => void;
  onReset: (key: string) => void;
  onSave: () => void;
  onDiscard: () => void;
  // onPictures is given the settings answer after a picture was uploaded or removed; onLocked says the server found one locked.
  onPictures: (answer: SettingsAnswer) => void;
  onLocked: () => void;
}

const TOKENS: [string, keyof ResolvedBrand["light"]][] = [
  ["accent", "accent"],
  ["accent_fill", "accentFill"],
  ["accent_fill_hover", "accentFillHover"],
  ["accent_fill_active", "accentFillActive"],
  ["accent_text", "accentText"],
  ["accent_soft", "accentSoft"],
  ["accent_border", "accentBorder"],
];
const SIDEBAR_KEYS = ["bg", "text", "caption", "active", "on_active"] as const;
const SIDEBAR_COLOUR = { bg: "bg", text: "text", caption: "caption", active: "active", on_active: "onActive" } as const;

const KINDS: PictureKind[] = ["logo", "icon"];

// hashOf is the hash of a picture in use, or undefined.
const hashOf = (p: Picture): string | undefined => (p.state === "uploaded" || p.state === "file" ? p.sha256 : undefined);

// BrandingCard is the Branding section: the name and the accent, the logo and the icon, the sidebar colours and theme tokens folded
// under Advanced, a preview of the brand with the unsaved values, and the section's Save. The preview reads the values being edited
// and the pictures in use, and changes nothing else on the page.
export default function BrandingCard({ section, settings, pictures, edits, errors, notice, dirty, busy, wide, onChange, onReset, onSave, onDiscard, onPictures, onLocked }: Props) {
  const { t } = useT();
  const byKey = useMemo(() => new Map(settings.map((s) => [s.key, s])), [settings]);
  // The value a field has now: the edit if there is one, else what the server holds.
  const now = (key: string): string => {
    const e = edits[key];
    if (e !== undefined && typeof e.value === "string") return e.value;
    const v = byKey.get(key)?.value;
    return typeof v === "string" ? v : "";
  };
  const picture = (kind: PictureKind): Picture => pictures.find((p) => p.key === `ui.brand.${kind}`) ?? { key: `ui.brand.${kind}`, state: "none", locked: false };
  const logo = pictureSrc("logo", picture("logo"));
  // The server draws the tab's icon from /favicon.svg and the app icons from /brand/icon-512.png, from the icon, or else the logo.
  const stamp = hashOf(picture("icon")) ?? hashOf(picture("logo"));
  const tab = stamp !== undefined ? `/favicon.svg?v=${stamp}` : null;
  const app = stamp !== undefined ? `/brand/icon-512.png?v=${stamp}` : null;

  const resolved = useMemo<ResolvedBrand>(() => {
    const given = (key: string) => {
      const v = now(key);
      return v !== "" && validAccent(v) ? v : undefined;
    };
    const tokens = (theme: "light" | "dark"): TokenWire => Object.fromEntries(TOKENS.map(([token]) => [token, given(`ui.brand.palette.${theme}.${token}`)]).filter(([, v]) => v !== undefined));
    const sidebar: SidebarWire = Object.fromEntries(SIDEBAR_KEYS.map((k) => [k, given(`ui.brand.sidebar.${k}`)]).filter(([, v]) => v !== undefined));
    const brand: Brand = {
      name: now("ui.brand.name"),
      accent: validAccent(now("ui.brand.accent")) ? now("ui.brand.accent") : DEFAULT_ACCENT,
      logo_url: logo,
      artwork: logo === null,
      palette: { light: tokens("light"), dark: tokens("dark") },
      sidebar,
    };
    return resolveBrand(brand);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [edits, settings, logo]);

  // The colour a colour field shows while it is empty: the one the interface would draw.
  const fallbackOf = (key: string): string => {
    if (key === "ui.brand.accent") return resolved.accent;
    const side = /^ui\.brand\.sidebar\.(\w+)$/.exec(key);
    if (side) return resolved.sidebar[SIDEBAR_COLOUR[side[1] as (typeof SIDEBAR_KEYS)[number]]];
    const pal = /^ui\.brand\.palette\.(light|dark)\.(\w+)$/.exec(key);
    const field = TOKENS.find(([token]) => token === pal?.[2])?.[1];
    return pal && field ? resolved[pal[1] as "light" | "dark"][field] : "#FFFFFF";
  };

  const row = (f: FieldSpec) => {
    const s = byKey.get(f.key);
    // A setting this server does not know is left out: the page draws what the server answers.
    if (s === undefined) return null;
    return <FieldRow key={f.key} spec={f} setting={s} edit={edits[f.key]} error={errors[f.key] ?? null} wide={wide} fallback={f.colour ? fallbackOf(f.key) : undefined} onChange={(v) => onChange(f.key, v)} onReset={() => onReset(f.key)} />;
  };

  const [main, ...folded] = section.groups;
  // A colour that fails the contrast check keeps the default in that theme (the interface's own check).
  const custom = resolved.accent !== DEFAULT_ACCENT;
  const warn = custom && !resolved.applies.light ? t("settings.brand.tooLight") : custom && !resolved.applies.dark ? t("settings.brand.tooDark") : null;

  return (
    <CardFrame id={section.id} title={t(`settings.section.${section.id}`)} intro={t(`settings.section.${section.id}.intro`)}>
      {main.fields.map(row)}
      {warn !== null && <Alert type="warning" showIcon style={{ marginBlock: 8 }} message={warn} />}
      {pictures.length > 0 && KINDS.map((kind) => <PictureRow key={kind} kind={kind} picture={picture(kind)} wide={wide} fill={kind === "logo" ? resolved.sidebar.bg : undefined} onAnswer={onPictures} onLocked={onLocked} />)}
      <Collapse
        ghost
        size="small"
        style={{ margin: "4px -12px 8px" }}
        items={folded.map((g) => ({ key: g.title ?? "advanced", label: t(g.title ?? "settings.group.advanced"), children: <div>{g.fields.map(row)}</div> }))}
      />
      <BrandPreview brand={resolved} logo={logo} tab={tab} app={app} />
      <SectionFoot notice={notice} dirty={dirty} busy={busy} onSave={onSave} onDiscard={onDiscard} />
    </CardFrame>
  );
}
