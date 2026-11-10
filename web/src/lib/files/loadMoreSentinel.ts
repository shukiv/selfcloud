// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";

// useLoadMoreSentinel asks for the next page when the element it returns comes into view (design 2.2, paging), so a list
// grows as its end is reached, and the Load more button stays beside it for the person who asks for it. The element is
// watched only while there is a next page (enabled) and no page is loading (loading), so a page is asked for once, and
// again only after the load ends with the end still in view.
export function useLoadMoreSentinel(enabled: boolean, loading: boolean, loadMore: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  const more = useRef(loadMore);
  useEffect(() => {
    more.current = loadMore;
  });
  useEffect(() => {
    const el = ref.current;
    if (!el || !enabled || loading || typeof IntersectionObserver === "undefined") return;
    const obs = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) more.current();
    });
    obs.observe(el);
    return () => obs.disconnect();
  }, [enabled, loading]);
  return ref;
}
