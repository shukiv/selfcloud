// SPDX-License-Identifier: AGPL-3.0-or-later

import { Button } from "antd";
import { Download } from "lucide-react";

// HeadDownload is the Download button of a player's head row (the viewer and a share page). Under 600 px it is its icon alone,
// as the Playlist button is: the name stays in aria-label, and the label is hidden by CSS, so a phone's head is one row.
export default function HeadDownload({ label, onClick }: { label: string; onClick: () => void }) {
  return (
    <Button className="media-head-download" aria-label={label} onClick={onClick} icon={<Download size={16} aria-hidden="true" />}>
      <span className="media-head-label">{label}</span>
    </Button>
  );
}
