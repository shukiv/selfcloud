// SPDX-License-Identifier: AGPL-3.0-or-later

import type { CSSProperties } from "react";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";

// The brand at the top of the sidebar (decision 109). Its block has the same edge as New's top (10 px, or 6 px on a finger), then
// the picture, then the same edge again, so New sits one edge under it. Expanded, it is the SelfCloud wordmark, the installation's
// logo, or its name as text. Folded to the rail, it is the mark (the artwork's, or the glyph for a custom name), or the logo small.
// The name of a custom installation is not drawn in the rail: it has no picture to fold to, so the glyph stands in for it.
export default function SidebarBrand({ collapsed, coarse, text }: { collapsed: boolean; coarse: boolean; text: string }) {
  const b = useStore(brand);
  const edge = coarse ? 6 : 10;
  let figure;
  if (b.artwork) {
    figure = collapsed ? mark(b.name, 32, text) : <img src="/brand/wordmark.svg" alt={b.name} height={38} />;
  } else if (b.logo_url) {
    figure = collapsed ? <img src={b.logo_url} alt={b.name} height={28} style={{ maxWidth: 40 }} /> : <img src={b.logo_url} alt={b.name} height={38} style={{ maxWidth: "100%" }} />;
  } else {
    figure = collapsed ? mark(b.name, 28, text) : <p style={{ margin: 0, color: text, fontSize: 18, fontWeight: 700, lineHeight: "38px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}><bdi>{b.name}</bdi></p>;
  }
  return (
    <div data-testid="sidebar-brand" style={{ flex: "none", display: "flex", alignItems: "center", justifyContent: collapsed ? "center" : "flex-start", minWidth: 0, paddingBlockStart: edge, marginBlockEnd: edge }}>
      {figure}
    </div>
  );
}

// mark is the mark as a mask in the text colour: the picture is the file's shape, so it takes the sidebar's ink (white on the default
// blue, and dark on a light brand).
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
