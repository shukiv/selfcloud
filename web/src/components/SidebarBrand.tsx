// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties } from "react";
import { useStore } from "../lib/store";
import { brand, brandLoaded, contrast, safeLogo } from "../lib/brand";

// The two pictures the default brand draws at the top of the sidebar, each reserved at its own ratio so the box never grows when the
// brand loads (decision 193). On a dark sidebar the white wordmark reads (/brand/wordmark.svg, internal/web/brand/selfcloud-logo-transparent.svg);
// on a light one it would not, so the tile logo is drawn (/brand/logo, internal/web/brand/selfcloud-logo.svg, viewBox 1731.65 x 398),
// which is taller at the same width. An uploaded logo (at most 42 px high) and the text name stay inside the reserved box.
const WORDMARK_RATIO = 38 / 211;
const LOGO_RATIO = 398 / 1731.65;

// The brand at the top of the sidebar (decision 109, its edge replaced by decision 193, the uploaded logo's centring replaced by
// decision 221). Its block takes the rows' inline margin, so it starts where the rows start, and the space above the picture and
// below the block is S, the sider's inline padding plus that margin, on a mouse and a finger alike. Expanded, it is the SelfCloud
// wordmark (every installation with no logo file, decision 126), the installation's logo, or, only from a server that sends neither,
// its name as text. The wordmark and the tile logo fill the block's width (decision 193); an uploaded logo keeps its aspect at the
// start edge, at most 42 px high (decision 221; flex mirrors the edge in Hebrew and Arabic). The block reserves the height of the
// picture the background will draw at its content width, so nothing moves when the brand arrives. Folded to the rail, it is the
// uploaded icon, the picture the favicon is drawn from (decision 221), else the logo kept in its own shape, else the SelfCloud mark
// for every name (decision 119), with the rail's own edges and centring unchanged.
// The name of a custom installation is not drawn in the rail: it has no picture to fold to, so the mark stands in for it. Nothing
// is drawn until the brand is loaded.
export default function SidebarBrand({ collapsed, coarse, text, background, inline, edge, width }: { collapsed: boolean; coarse: boolean; text: string; background: string; inline: number; edge: number; width: number }) {
  const b = useStore(brand);
  const loaded = useStore(brandLoaded);
  const logo = safeLogo(b.logo_url);
  const icon = safeLogo(b.icon_url);
  // The wordmark has white letters, which read only on a dark sidebar; on a light one the tile logo is drawn instead (decision 109).
  const wordmarkReads = contrast("#FFFFFF", background) >= 3;
  // The reserved height is the picture the background will draw, at the block's content width, rounded up (decision 193): the
  // wordmark on a dark sidebar, the taller tile logo on a light one, so the box keeps its height when the brand loads.
  const reserved = Math.ceil((width - 2 * edge) * (wordmarkReads ? WORDMARK_RATIO : LOGO_RATIO));
  const railEdge = coarse ? 6 : 10;
  // The rail centres its picture; expanded, the wordmark and the tile fill the width, and the uploaded logo and the name sit at
  // the start edge (decision 221 replaces decision 193's centring for uploaded logos).
  const centred = collapsed;
  let figure = null;
  if (!loaded) {
    figure = null;
  } else if (collapsed) {
    // The rail shows the icon at 28 by 28, kept in shape; else the logo in a box at most 40 wide and 28 high, never stretched;
    // else the mark, the artwork's at 32 and a name with no picture at 28 (decision 221).
    figure = icon ? (
      <img src={icon} alt={b.name} width={28} height={28} style={{ objectFit: "contain", display: "block" }} />
    ) : logo ? (
      <img src={logo} alt={b.name} style={{ maxWidth: 40, maxHeight: 28, objectFit: "contain", width: "auto", height: "auto", display: "block" }} />
    ) : (
      mark(b.name, b.artwork ? 32 : 28, text)
    );
  } else if (b.artwork) {
    figure = <img src={wordmarkReads ? "/brand/wordmark.svg" : "/brand/logo"} alt={b.name} style={{ width: "100%", height: "auto", display: "block" }} />;
  } else if (logo) {
    figure = <img src={logo} alt={b.name} style={{ maxWidth: "100%", maxHeight: 42, objectFit: "contain", width: "auto", height: "auto", display: "block" }} />;
  } else {
    figure = <p style={{ margin: 0, color: text, fontSize: 18, fontWeight: 700, lineHeight: "38px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}><bdi>{b.name}</bdi></p>;
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
