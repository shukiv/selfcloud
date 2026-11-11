// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useRef } from "react";

// useLoadMoreSentinel asks for the next page when the element it returns comes into view (design 2.2, paging), so a list
// grows as its end is reached, and the Load more button stays beside it for the person who asks for it. The element is
// watched only while there is a next page (enabled), no page is loading (loading), and the last next page did not fail
// (failed). So a page is asked for once, and again only after the load ends with the end still in view. A failed page is not
// asked for again by the sentinel: an observer would fire at once while the end is in view, and the request would repeat
// with no delay. Only Load more tries again, and a load that succeeds re-arms the sentinel.
export function useLoadMoreSentinel(enabled: boolean, loading: boolean, failed: boolean, loadMore: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  const more = useRef(loadMore);
  useEffect(() => {
    more.current = loadMore;
  });
  useEffect(() => {
    const el = ref.current;
    if (!el || !enabled || loading || failed || typeof IntersectionObserver === "undefined") return;
    const obs = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) more.current();
    });
    obs.observe(el);
    return () => obs.disconnect();
  }, [enabled, loading, failed]);
  return ref;
}
