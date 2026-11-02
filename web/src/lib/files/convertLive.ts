// SPDX-License-Identifier: AGPL-3.0-or-later

// The browser's own parts of a preview or export watch (convert.ts holds the logic, and its tests replace these).

import { api } from "../api";
import { exportPath, type ExportDeps, type PreviewDeps } from "./convert";
import { startDownloadURL } from "./ops";

export function livePreviewDeps(): PreviewDeps {
  return {
    fetch: (input, init) => fetch(input, init),
    schedule: (fn, ms) => setTimeout(fn, ms),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  };
}

export function liveExportDeps(): ExportDeps {
  return {
    post: (nod, format) => api<{ id: string }>("POST", `/api/v1/nodes/${nod}/exports`, { body: { format } }),
    head: async (nod, id) => {
      const res = await fetch(exportPath(nod, id), { method: "HEAD", credentials: "same-origin", cache: "no-store" });
      const retry = Number(res.headers.get("Retry-After"));
      return { status: res.status, retryAfter: Number.isFinite(retry) && retry > 0 ? retry : undefined };
    },
    schedule: (fn, ms) => setTimeout(fn, ms),
    cancel: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
  };
}

// downloadExport starts the download of a ready export: the browser saves it, and resumes it if it is interrupted.
export function downloadExport(href: string): Promise<boolean> {
  return startDownloadURL(href);
}
