// SPDX-License-Identifier: AGPL-3.0-or-later

import { Download } from "lucide-react";
import IconButton from "../IconButton";

// HeadDownload is the Download icon of a player's head row (the viewer and a share page). Its name is its label and its
// tooltip (decision 68).
export default function HeadDownload({ label, onClick }: { label: string; onClick: () => void }) {
  return <IconButton className="media-head-download" label={label} onClick={onClick} icon={<Download size={18} aria-hidden="true" focusable="false" />} />;
}
