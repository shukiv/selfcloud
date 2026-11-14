// SPDX-License-Identifier: AGPL-3.0-or-later

// playerBox is the one box the media session's player lives in (decision 172). The viewer, the Media page's stage and the
// hidden parking place each hold the same box at different times: moving the box moves the player with it, so the element
// that plays is never made again and never stops for a change of place. The box is made once, on first use, and kept for
// the whole page; React draws the session's header and playlist into it through a portal (MediaDock).

let box: HTMLDivElement | null = null;
let parking: HTMLDivElement | null = null;

// playerBox returns the session's box, making it once. It is a flex column with the viewer's gap, so the header and the
// player keep the same room they had in the dialog.
export function playerBox(): HTMLDivElement {
  if (box === null) {
    box = document.createElement("div");
    box.style.display = "flex";
    box.style.flexDirection = "column";
    box.style.gap = "12px";
    box.style.flex = "1 1 auto";
    box.style.minHeight = "0";
  }
  return box;
}

// Mover is the state-preserving move (Chrome's moveBefore): it takes the box to the target without the remove and insert
// that may pause a playing file. It is new, so its type is read here rather than assumed of every browser.
type Mover = HTMLElement & { moveBefore?: (node: Node, ref: Node | null) => void };

// placeBox moves the box into the target, keeping the file playing. A move that pauses a file that played is started
// again; a refusal of that start is swallowed (the player shows its own press-Play note when it matters). A target that
// already holds the box is left alone.
export function placeBox(target: HTMLElement): void {
  const b = playerBox();
  if (b.parentElement === target) return;
  const media = b.querySelector<HTMLMediaElement>("audio, video");
  const played = media !== null && !media.paused && !media.ended;
  const mover = target as Mover;
  if (typeof mover.moveBefore === "function" && b.isConnected && target.isConnected) {
    try {
      mover.moveBefore(b, null);
    } catch {
      target.appendChild(b);
    }
  } else {
    target.appendChild(b);
  }
  if (played && media !== null && media.paused) {
    Promise.resolve(media.play()).catch(() => undefined);
  }
}

// parkBox keeps the box in one hidden place on the body while no page holds it: the session's element stays in the page,
// so audio goes on in the mini player, and the next placement takes the box from here.
export function parkBox(): void {
  const b = playerBox();
  if (parking === null) {
    parking = document.createElement("div");
    parking.style.display = "none";
    document.body.appendChild(parking);
  }
  if (b.parentElement !== parking) parking.appendChild(b);
}
