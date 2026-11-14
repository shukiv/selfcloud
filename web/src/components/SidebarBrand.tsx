// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties } from "react";
import { useStore } from "../lib/store";
import { brand, brandLoaded, contrast, safeLogo } from "../lib/brand";

// The brand at the top of the sidebar (decision 109, its edge replaced by decision 185). Its block takes the rows' inline margin, so
// it starts where the rows start, and the space above the picture and below the block is S, the sider's inline padding plus that
// margin, on a mouse and a finger alike. Expanded, it is the SelfCloud wordmark (every installation with no logo file, decision 126),
// the installation's logo, or, only from a server that sends neither, its name as text. The wordmark and the tile logo fill the
// block's width (decision 185); an uploaded logo keeps its aspect, centred, at most 42 px high. The block reserves the wordmark's
// height at its content width, so nothing moves when the brand arrives. Folded to the rail, it is the SelfCloud mark for every name
// (decision 119), or the logo small, with the rail's own edges unchanged.
// The name of a custom installation is not drawn in the rail: it has no picture to fold to, so the mark stands in for it. Nothing
// is drawn until the brand is loaded.
export default function SidebarBrand({ collapsed, coarse, text, background, inline, edge, width }: { collapsed: boolean; coarse: boolean; text: string; background: string; inline: number; edge: number; width: number }) {
  const b = useStore(brand);
  const loaded = useStore(brandLoaded);
  const logo = safeLogo(b.logo_url);
  // The wordmark has white letters, which read only on a dark sidebar; on a light one the tile logo is drawn instead (decision 109).
  const wordmarkReads = contrast("#FFFFFF", background) >= 3;
  // The reserved height is the wordmark's own ratio (211 x 38) at the block's content width, rounded up (decision 185).
  const reserved = Math.ceil(((width - 2 * edge) * 38) / 211);
  const railEdge = coarse ? 6 : 10;
  // The uploaded logo is centred; the wordmark and the tile fill the width, and the name sits at the start edge.
  const centred = collapsed || (!b.artwork && !!logo);
  let figure = null;
  if (!loaded) {
    figure = null;
  } else if (b.artwork) {
    figure = collapsed ? mark(b.name, 32, text) : <img src={wordmarkReads ? "/brand/wordmark.svg" : "/brand/logo"} alt={b.name} style={{ width: "100%", height: "auto", display: "block" }} />;
  } else if (logo) {
    figure = collapsed ? <img src={logo} alt={b.name} height={28} style={{ maxWidth: 40 }} /> : <img src={logo} alt={b.name} style={{ maxWidth: "100%", maxHeight: 42, objectFit: "contain", width: "auto", height: "auto", display: "block" }} />;
  } else {
    figure = collapsed ? mark(b.name, 28, text) : <p style={{ margin: 0, color: text, fontSize: 18, fontWeight: 700, lineHeight: "38px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}><bdi>{b.name}</bdi></p>;
  }
  return (
    <div data-testid="sidebar-brand" style={{ flex: "none", display: "flex", alignItems: "center", justifyContent: centred ? "center" : "flex-start", minWidth: 0, minHeight: collapsed ? 32 : reserved, marginInline: collapsed ? undefined : inline, paddingBlockStart: collapsed ? railEdge : edge, marginBlockEnd: collapsed ? railEdge : edge }}>
      {figure}
    </div>
  );
}

// mark is the mark as a mask in the text colour: the picture is the file's shape, so it takes the sidebar's ink. The ink is chosen
// against the sidebar (text is textOn the sidebar), so the mark reads on a light brand as well as on the default blue.
function mark(name: string, size: number, colour: string) {
  const url = "url(/brand/mark.svg)";
  const style: CSSProperties = {
    display: "block",
    width: size,
    height: size,
    background: colour,
    WebkitMask: `${url} center / contain no-repeat`,
    mask: `${url} center / contain no-repeat`,
  };
  return <span role="img" aria-label={name} style={style} />;
}
