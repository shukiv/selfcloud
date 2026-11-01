// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";

// useCoarsePointer says whether the main pointer is a finger, so a screen without a mouse gets the checkboxes a mouse
// does without. It follows the media query, not the width of the window: a narrow desktop window keeps its mouse.
export function useCoarsePointer(): boolean {
  const supported = typeof window !== "undefined" && typeof window.matchMedia === "function";
  const [coarse, setCoarse] = useState(() => (supported ? window.matchMedia("(pointer: coarse)").matches : false));
  useEffect(() => {
    if (!supported) return;
    const mq = window.matchMedia("(pointer: coarse)");
    const onChange = () => setCoarse(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [supported]);
  return coarse;
}

