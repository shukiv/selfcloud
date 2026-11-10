// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties } from "react";
import { useStore } from "../lib/store";
import { brand, brandLoaded, contrast, safeLogo } from "../lib/brand";

// The brand at the top of the sidebar (decision 109). Its block has the same edge as New's top (10 px, or 6 px on a finger), then
// the picture, then the same edge again, so New sits one edge under it. Expanded, it is the SelfCloud wordmark (every installation
// with no logo file, decision 126), the installation's logo, or, only from a server that sends neither, its name as text. Folded to the rail, it is the SelfCloud mark for every name (decision 119), or the logo small.
// The name of a custom installation is not drawn in the rail: it has no picture to fold to, so the mark stands in for it. Nothing
// is drawn until the brand is loaded, and the picture's space is kept so nothing moves.
export default function SidebarBrand({ collapsed, coarse, text, background }: { collapsed: boolean; coarse: boolean; text: string; background: string }) {
  const b = useStore(brand);
  const loaded = useStore(brandLoaded);
  const edge = coarse ? 6 : 10;
  const logo = safeLogo(b.logo_url);
  // The wordmark has white letters, which read only on a dark sidebar; on a light one the tile logo is drawn instead (decision 109).
  const wordmarkReads = contrast("#FFFFFF", background) >= 3;
  let figure = null;
  if (!loaded) {
    figure = null;
  } else if (b.artwork) {
    figure = collapsed ? mark(b.name, 32, text) : wordmarkReads ? <img src="/brand/wordmark.svg" alt={b.name} height={38} /> : <img src="/brand/logo" alt={b.name} height={38} />;
  } else if (logo) {
    figure = collapsed ? <img src={logo} alt={b.name} height={28} style={{ maxWidth: 40 }} /> : <img src={logo} alt={b.name} height={38} style={{ maxWidth: "100%" }} />;
  } else {
    figure = collapsed ? mark(b.name, 28, text) : <p style={{ margin: 0, color: text, fontSize: 18, fontWeight: 700, lineHeight: "38px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}><bdi>{b.name}</bdi></p>;
  }
  return (
    <div data-testid="sidebar-brand" style={{ flex: "none", display: "flex", alignItems: "center", justifyContent: collapsed ? "center" : "flex-start", minWidth: 0, minHeight: collapsed ? 32 : 38, paddingBlockStart: edge, marginBlockEnd: edge }}>
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
