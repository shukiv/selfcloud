// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useState } from "react";
import { Button } from "antd";
import { useQuery } from "@tanstack/react-query";
import { Bell as BellIcon } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { unreadCount } from "../../lib/notifications/api";
import { badgeText, pollInterval, unreadState } from "../../lib/notifications/logic";
import { useNT } from "../../lib/notifications/i18n";
import { queryKey } from "../../lib/query";
import { useCoarsePointer } from "../../lib/pointer";
import { RADIUS } from "../../theme/tokens";

// Bell is the top bar's entry to the notifications page, with the count of unread notifications. The count is read when
// the bar appears in a tab that is shown, once a minute while it stays shown, and again when the tab comes back. A tab
// that is hidden when the bar appears makes no request at all: the first read waits until the tab is shown. A failed read
// keeps the count it had and shows no error. The count is in a polite live region, so a screen reader hears a change, and
// the badge, the button's name and the live region all show the same number.
export default function Bell() {
  const { t, ready } = useNT();
  const coarse = useCoarsePointer();
  const navigate = useNavigate();
  const [visible, setVisible] = useState(() => document.visibilityState !== "hidden");
  useEffect(() => {
    const onChange = () => setVisible(document.visibilityState !== "hidden");
    document.addEventListener("visibilitychange", onChange);
    return () => document.removeEventListener("visibilitychange", onChange);
  }, []);
  const q = useQuery({
    queryKey: queryKey("notifications", "unread"),
    queryFn: unreadCount,
    enabled: visible,
    staleTime: 0,
    retry: false,
    refetchInterval: () => pollInterval(document.visibilityState === "hidden"),
    refetchIntervalInBackground: false,
    refetchOnWindowFocus: true,
  });
  // The catalogue is a chunk of its own: the bell draws nothing until its words are there.
  if (!ready) return null;
  const count = q.data ? q.data.count : 0;
  const more = !!q.data?.more;
  const badge = q.data ? badgeText(count, more) : null;
  const shown = q.data ? t(unreadState(count, more).key, unreadState(count, more).params) : "";
  // The badge is a plain span, not antd's Badge: Badge would pull antd modules into the first load, which the bell does not
  // need. The number is text, so the count is never shown by colour alone.
  return (
    <>
      <span role="status" aria-live="polite" className="sr-only">
        {shown}
      </span>
      <span style={{ position: "relative", display: "inline-flex" }}>
        <Button
          type="text"
          data-testid="notifications-bell"
          aria-label={badge ? `${t("notifications.bell")}, ${shown}` : t("notifications.bell")}
          onClick={() => navigate("/notifications")}
          icon={<BellIcon size={18} />}
          style={{ borderRadius: RADIUS, minWidth: 44, minHeight: coarse ? 44 : undefined }}
        />
        {badge && (
          <span
            aria-hidden="true"
            data-testid="notifications-count"
            style={{
              position: "absolute",
              insetBlockStart: 2,
              insetInlineEnd: 2,
              minWidth: 18,
              height: 18,
              padding: "0 5px",
              borderRadius: 9,
              fontSize: 11,
              lineHeight: "18px",
              textAlign: "center",
              pointerEvents: "none",
              background: "var(--ant-color-error, #d4380d)",
              color: "#fff",
            }}
          >
            {badge}
          </span>
        )}
      </span>
    </>
  );
}
