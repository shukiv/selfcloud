// SPDX-License-Identifier: AGPL-3.0-or-later

import { useLang, useT, type Params } from "../../lib/i18n";
import { formatBytes } from "../../lib/files/format";
import { TYPE_COLOURS } from "../../lib/files/iconType";
import { heldBytes, type MeStorage } from "../../lib/storage/logic";
import { meterSegments, type Breakdown, type Segments } from "../../lib/storage/breakdown";

// The colours of the bar's parts (decision 79). Files take the brand's primary text token, which is the accent that reads
// on the track in dark mode (the primary fill is too dark there: 2.9:1); the others take the type colours, which pass 3:1
// against the page and the card in both modes (iconType.test.ts). The free part is the bar's own track.
export const SEGMENT_COLOURS = {
  files: "var(--ant-color-primary-text)",
  versions: TYPE_COLOURS.blue,
  trash: TYPE_COLOURS.orange,
  reserved: TYPE_COLOURS.grey,
  free: "var(--ant-color-fill-secondary)",
} as const;

// StorageMeter is the Storage page's meter card: the amount My files holds, the limit and what is free, one segmented bar
// with its parts, and a legend with each part's size. The bar has a text alternative that says the whole breakdown.
export default function StorageMeter({ figures, breakdown }: { figures: MeStorage; breakdown: Breakdown }) {
  const { t } = useT();
  const lang = useLang();
  const held = heldBytes(figures);
  const seg = meterSegments(figures, breakdown);
  const limit = figures.quota_bytes !== null && figures.quota_bytes > 0 ? figures.quota_bytes : null;
  const total = limit ?? Math.max(held, 1);
  const parts = partsOf(seg, lang, t);
  const alt = parts.map((p) => p.text).join(", ");
  const sub = limit === null
    ? t("storage.meter.noLimit")
    : t("storage.meter.ofLimit", { limit: formatBytes(limit, lang), free: formatBytes(seg.free ?? 0, lang) });
  const altText = limit === null
    ? t("storage.meter.altNoLimit", { used: formatBytes(held, lang), parts: alt })
    : t("storage.meter.altLimit", { used: formatBytes(held, lang), limit: formatBytes(limit, lang), parts: alt });

  return (
    <section aria-labelledby="storage-meter" className="storage-card">
      <div style={{ display: "flex", flexWrap: "wrap", alignItems: "flex-end", justifyContent: "space-between", gap: 12 }}>
        <div>
          <div className="num" style={{ fontSize: 32, lineHeight: "40px", fontWeight: 600 }}>
            {formatBytes(held, lang)}
          </div>
          <div style={{ color: "var(--ant-color-text-secondary)" }}>{sub}</div>
        </div>
        <span id="storage-meter" style={{ color: "var(--ant-color-text-secondary)", fontSize: 13 }}>
          {t("storage.mine")}
        </span>
      </div>

      <div
        role="img"
        aria-label={altText}
        className="storage-bar"
        style={{ display: "flex", gap: 2, height: 12, borderRadius: 6, overflow: "hidden", background: SEGMENT_COLOURS.free, margin: "16px 0" }}
      >
        {parts
          .filter((p) => p.bytes > 0)
          .map((p) => (
            <span
              key={p.key}
              data-part={p.key}
              style={{ display: "block", flex: "none", width: `${Math.max(0.5, (p.bytes / total) * 100)}%`, minWidth: 4, height: "100%", background: p.colour }}
            />
          ))}
      </div>

      <ul className="storage-legend" style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexWrap: "wrap", gap: "12px 28px" }}>
        {parts.map((p) => (
          <li key={p.key} style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
            <span aria-hidden="true" style={{ width: 10, height: 10, borderRadius: 5, background: p.colour, flex: "none" }} />
            <span style={{ color: "var(--ant-color-text-secondary)" }}>{p.label}</span>
            <span className="num" style={{ fontWeight: 600 }}>
              {formatBytes(p.bytes, lang)}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

interface Part {
  key: keyof typeof SEGMENT_COLOURS;
  label: string;
  bytes: number;
  colour: string;
  text: string;
}

// partsOf lists the parts in the bar's order, with the legend's label and the words of the text alternative.
function partsOf(seg: Segments, lang: string, t: (key: string, params?: Params) => string): Part[] {
  const size = (n: number) => formatBytes(n, lang);
  const list: Part[] = [
    { key: "files", label: t("storage.files"), bytes: seg.files, colour: SEGMENT_COLOURS.files, text: t("storage.meter.partFiles", { size: size(seg.files) }) },
    { key: "versions", label: t("storage.oldVersions"), bytes: seg.versions, colour: SEGMENT_COLOURS.versions, text: t("storage.meter.partVersions", { size: size(seg.versions) }) },
    { key: "trash", label: t("storage.inTrash"), bytes: seg.trash, colour: SEGMENT_COLOURS.trash, text: t("storage.meter.partTrash", { size: size(seg.trash) }) },
    { key: "reserved", label: t("storage.reserved"), bytes: seg.reserved, colour: SEGMENT_COLOURS.reserved, text: t("storage.meter.partReserved", { size: size(seg.reserved) }) },
  ];
  if (seg.free !== null) {
    list.push({ key: "free", label: t("storage.free"), bytes: seg.free, colour: SEGMENT_COLOURS.free, text: t("storage.meter.partFree", { size: size(seg.free) }) });
  }
  return list;
}
