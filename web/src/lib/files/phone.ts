// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";

// PHONE_QUERY is the one width the album-page player uses (mp7): a window no wider than 599 px. It is the same break the
// styles use, so the layout the person sees and the parts the player draws change together. Over it nothing of the phone
// layout is drawn.
export const PHONE_QUERY = "(max-width: 599px)";

// usePhoneWidth says whether the window is at phone width, and follows a resize. It reads the media query, not the element
// size, so it is the same for the whole session wherever the player is drawn (the viewer or the Media page).
export function usePhoneWidth(): boolean {
  const supported = typeof window !== "undefined" && typeof window.matchMedia === "function";
  const [phone, setPhone] = useState(() => (supported ? window.matchMedia(PHONE_QUERY).matches : false));
  useEffect(() => {
    if (!supported) return;
    const mq = window.matchMedia(PHONE_QUERY);
    const onChange = () => setPhone(mq.matches);
    onChange();
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [supported]);
  return phone;
}