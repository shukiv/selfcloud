// SPDX-License-Identifier: AGPL-3.0-or-later

// Up and Down in the tile view. The tiles sit in bands (folders first, then files), each band a grid of its own, so a
// count of columns does not say where the tile above or below is. The page reads each tile's place on the screen and asks
// this function which tile is one row away.

export interface TilePlace {
  // top is the tile's top edge on the screen, and x its edge on the horizontal axis, as the page measures it, so a
  // right-to-left layout is read the same way.
  top: number;
  x: number;
  width: number;
}

// ROW_TOLERANCE is how far apart two tops may be and still be one row (sub-pixel layout).
const ROW_TOLERANCE = 2;

// nextRow returns the index of the tile one row up (dir -1) or down (dir 1) from tile index: the tile in the nearest row that
// way whose centre is nearest the centre of the tile now. That is the same column when the row has one, else the nearest
// column. It returns index itself when there is no row that way. Rows are read from the screen, so a band of another
// width, or a partial last row, is followed as it is shown.
export function nextRow(tiles: readonly TilePlace[], index: number, dir: 1 | -1): number {
  const here = tiles[index];
  if (!here) return index;
  // The nearest top beyond this tile's row, in the direction asked.
  let rowTop: number | null = null;
  for (const t of tiles) {
    const step = (t.top - here.top) * dir;
    if (step <= ROW_TOLERANCE) continue;
    if (rowTop === null || (t.top - rowTop) * dir < 0) rowTop = t.top;
  }
  if (rowTop === null) return index;
  const mid = here.x + here.width / 2;
  let best = index;
  let bestGap = Infinity;
  tiles.forEach((t, i) => {
    if (Math.abs(t.top - rowTop) > ROW_TOLERANCE) return;
    const gap = Math.abs(t.x + t.width / 2 - mid);
    if (gap < bestGap) {
      bestGap = gap;
      best = i;
    }
  });
  return best;
}

// rowStep is the step in the flat order from tile index one row up (dir -1) or down (dir 1): nextRow's answer less index. A
// tile of the order that is not on the screen (null) makes the step zero: no count of columns stands in for it, so the focus
// stays where it is rather than jumping to a tile that is not the one below.
export function rowStep(places: readonly (TilePlace | null)[], index: number, dir: 1 | -1): number {
  if (places.some((p) => p === null)) return 0;
  return nextRow(places as readonly TilePlace[], index, dir) - index;
}
