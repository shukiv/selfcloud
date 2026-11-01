// SPDX-License-Identifier: AGPL-3.0-or-later

// copyText puts text on the system clipboard and says whether it did. The clipboard API is for a secure page (https, or
// localhost); on a plain http address the browser has none, and the old copy command on a selected text is tried instead.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Refused (no permission, or the page not in focus): the old way is tried next.
  }
  const before = document.activeElement instanceof HTMLElement ? document.activeElement : null;
  const box = document.createElement("textarea");
  try {
    box.value = text;
    box.setAttribute("readonly", "");
    box.setAttribute("aria-hidden", "true");
    box.style.position = "fixed";
    box.style.insetBlockStart = "0";
    box.style.opacity = "0";
    document.body.appendChild(box);
    box.select();
    return document.execCommand("copy") === true;
  } catch {
    return false;
  } finally {
    box.remove();
    before?.focus();
  }
}

// nodeAddress is this app's own address for an item: the page a signed-in person opens to see it in its folder. It is not a
// share link, and gives nobody access; whoever opens it needs to be able to reach the item already.
export function nodeAddress(id: string): string {
  return `${window.location.origin}/n/${id}`;
}
