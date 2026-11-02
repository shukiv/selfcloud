// SPDX-License-Identifier: AGPL-3.0-or-later

// Document previews and exports (decision 59, on the server's decision 37). A preview is the document as a PDF, shown in
// a frame; an export is the document in another format, downloaded. Both are made by the server, which takes time, so the
// page asks again until the answer is final. Every message is chosen by the failure's name: the server's text, and any ID, is
// never shown. A watch never waits for ever: after MAX_CONVERT_WAITS asks it says the conversion is slow.

import { ApiError } from "../apiError";
import { writable } from "../store";
import { waitMs } from "./thumbnail";

// MAX_CONVERT_WAITS is the most times a watch asks again after a 202. At the longest wait of thumbnail.ts that is about
// three minutes, and then the page says so.
export const MAX_CONVERT_WAITS = 90;

// The reasons a conversion does not give a file. Each has its own text on the page.
export type Failure = "failed" | "notConvertible" | "unavailable" | "restricted" | "tooLarge";

// failureKey is the catalogue key of a failure's text. The page shows the text by the failure's name, never the server's.
export function failureKey(why: Failure): string {
  switch (why) {
    case "notConvertible":
      return "files.convert.notConvertible";
    case "unavailable":
      return "files.convert.unavailable";
    case "restricted":
      return "files.err.downloadRestricted";
    case "tooLarge":
      return "files.convert.tooLarge";
    default:
      return "files.convert.failed";
  }
}

// ConvertHandlers receive what the page shows. Exactly one of ready, failed and slow is called, unless the watch is stopped.
export interface ConvertHandlers {
  making: () => void;
  // ready gives the address that shows the file: a preview's address for a frame, or an export's address for a download.
  ready: (href: string) => void;
  failed: (why: Failure) => void;
  slow: () => void;
}

// failureOf reads a refusal by its status and its reason. reason is the server's reason from the body, which a HEAD does not
// carry: a 415 that arrives without one is a failed conversion, since the only other 415 comes before any row exists.
export function failureOf(status: number, reason?: string): Failure {
  switch (status) {
    case 403:
      return "restricted";
    case 413:
      return "tooLarge";
    case 415:
      return reason === "not_convertible" ? "notConvertible" : "failed";
    case 429:
    case 503:
      return "unavailable";
    default:
      return "failed";
  }
}

// The answer a watch needs from one request: the status, the Response type (an opaque redirect has no status), the Retry-After
// header and the refusal's reason.
export interface Answer {
  status: number;
  type?: string;
  retryAfter?: string | null;
  reason?: string;
}

// PreviewStep is what one answer means for a preview watch.
export type PreviewStep = { kind: "ready" } | { kind: "wait"; ms: number } | { kind: "failed"; failure: Failure };

// previewStep reads one answer. A 200, or a redirect the browser does not follow (content_url set: the file is on the content
// origin), is ready. A 202 is a wait. Anything else is a failure.
export function previewStep(a: Answer): PreviewStep {
  if (a.type === "opaqueredirect" || a.status === 200) return { kind: "ready" };
  if (a.status === 202) return { kind: "wait", ms: waitMs(a.retryAfter ?? null) };
  return { kind: "failed", failure: failureOf(a.status, a.reason) };
}

// PreviewDeps are the parts a test replaces: the request, and the timers.
export interface PreviewDeps {
  fetch: typeof fetch;
  schedule: (fn: () => void, ms: number) => unknown;
  cancel: (handle: unknown) => void;
}

// previewPath is the same-origin address of a preview as PDF.
export function previewPath(nod: string): string {
  return `/api/v1/nodes/${nod}/preview?format=pdf`;
}

// reasonOf reads the refusal's reason from a body, when the body is JSON and has one. A body that is missing or not JSON gives none.
async function reasonOf(res: Response): Promise<string | undefined> {
  try {
    const body = (await res.json()) as { error?: { details?: { reason?: unknown } } };
    const r = body?.error?.details?.reason;
    return typeof r === "string" ? r : undefined;
  } catch {
    return undefined;
  }
}

// watchPreview asks for the PDF preview of a node until the answer is final. The first request starts the conversion; later
// requests are HEAD requests, which start no work. It returns a function that stops the watch: a stopped watch asks no more
// and reports nothing.
export function watchPreview(nod: string, h: ConvertHandlers, deps: PreviewDeps): () => void {
  let stopped = false;
  let handle: unknown = null;
  let waits = 0;
  const path = previewPath(nod);

  const finish = (step: PreviewStep) => {
    if (stopped) return;
    if (step.kind === "ready") h.ready(path);
    else if (step.kind === "failed") h.failed(step.failure);
  };

  const again = (ms: number) => {
    if (++waits > MAX_CONVERT_WAITS) {
      if (!stopped) h.slow();
      return;
    }
    handle = deps.schedule(poll, ms);
  };

  const handleAnswer = (step: PreviewStep) => {
    if (step.kind === "wait") again(step.ms);
    else finish(step);
  };

  // first starts the conversion. A 200 is answered with its body dropped: the page shows the file through the frame.
  const first = async () => {
    handle = null;
    if (stopped) return;
    h.making();
    let res: Response;
    try {
      res = await deps.fetch(path, { credentials: "same-origin", cache: "no-store", redirect: "manual" });
    } catch {
      finish({ kind: "failed", failure: "failed" });
      return;
    }
    if (stopped) return;
    if (res.status === 200) void res.body?.cancel();
    const reason = res.status === 415 ? await reasonOf(res) : undefined;
    if (stopped) return;
    handleAnswer(previewStep({ status: res.status, type: res.type, retryAfter: res.headers.get("Retry-After"), reason }));
  };

  // poll asks, with HEAD, whether the preview is ready.
  const poll = async () => {
    handle = null;
    if (stopped) return;
    let res: Response;
    try {
      res = await deps.fetch(path, { method: "HEAD", credentials: "same-origin", cache: "no-store", redirect: "manual" });
    } catch {
      finish({ kind: "failed", failure: "failed" });
      return;
    }
    if (stopped) return;
    handleAnswer(previewStep({ status: res.status, type: res.type, retryAfter: res.headers.get("Retry-After") }));
  };

  handle = deps.schedule(first, 0);
  return () => {
    if (stopped) return;
    stopped = true;
    if (handle !== null) deps.cancel(handle);
    handle = null;
  };
}

// exportFormatsFor lists the formats a document can be exported to: PDF, and the formats of its own family. A legacy binary
// format is offered as PDF only; a document of another kind has none. Cross-family conversions fail with the converter
// (decision 37), so they are not offered.
export function exportFormatsFor(name: string): string[] {
  const ext = name.includes(".") ? name.slice(name.lastIndexOf(".") + 1).toLowerCase() : "";
  switch (ext) {
    case "docx":
    case "odt":
      return ["pdf", ext === "docx" ? "docx" : "odt", ext === "docx" ? "odt" : "docx"];
    case "xlsx":
    case "ods":
      return ["pdf", ext === "xlsx" ? "xlsx" : "ods", ext === "xlsx" ? "ods" : "xlsx"];
    case "pptx":
    case "odp":
      return ["pdf", ext === "pptx" ? "pptx" : "odp", ext === "pptx" ? "odp" : "pptx"];
    case "doc":
    case "xls":
    case "ppt":
      return ["pdf"];
    default:
      return [];
  }
}

// ExportDeps are the parts a test replaces: the request that queues an export, the poll, and the timers.
export interface ExportDeps {
  post: (nod: string, format: string) => Promise<{ id: string }>;
  head: (nod: string, id: string) => Promise<{ status: number; retryAfter?: number }>;
  schedule: (fn: () => void, ms: number) => unknown;
  cancel: (handle: unknown) => void;
}

// exportPath is the same-origin address of an export made for this node.
export function exportPath(nod: string, id: string): string {
  return `/api/v1/nodes/${nod}/exports/${id}`;
}

// failureOfError reads a refused queueing: an ApiError carries the status and the reason; anything else is a failed conversion.
export function failureOfError(err: unknown): Failure {
  if (err instanceof ApiError) {
    const reason = typeof err.details.reason === "string" ? err.details.reason : undefined;
    return failureOf(err.status, reason);
  }
  return "failed";
}

// startExport queues an export of a node in format, then polls its ID until the file is ready. It returns a function that stops
// the watch: the server finishes the export, and nothing is downloaded by a stopped watch.
export function startExport(nod: string, format: string, h: ConvertHandlers, deps: ExportDeps): () => void {
  let stopped = false;
  let handle: unknown = null;
  let waits = 0;

  const again = (ms: number) => {
    if (++waits > MAX_CONVERT_WAITS) {
      if (!stopped) h.slow();
      return;
    }
    handle = deps.schedule(() => void poll(id), ms);
  };

  let id = "";
  const poll = async (drv: string) => {
    handle = null;
    if (stopped) return;
    let res: { status: number; retryAfter?: number };
    try {
      res = await deps.head(nod, drv);
    } catch {
      if (!stopped) h.failed("failed");
      return;
    }
    if (stopped) return;
    if (res.status === 202) {
      again(waitMs(res.retryAfter === undefined ? null : String(res.retryAfter)));
      return;
    }
    if (res.status === 200) {
      h.ready(exportPath(nod, drv));
      return;
    }
    h.failed(failureOf(res.status));
  };

  // queue asks for the export. A refusal here is the page's failure at once.
  const queue = async () => {
    handle = null;
    if (stopped) return;
    h.making();
    let got: { id: string };
    try {
      got = await deps.post(nod, format);
    } catch (err) {
      if (!stopped) h.failed(failureOfError(err));
      return;
    }
    if (stopped) return;
    id = got.id;
    void poll(id);
  };

  handle = deps.schedule(() => void queue(), 0);
  return () => {
    if (stopped) return;
    stopped = true;
    if (handle !== null) deps.cancel(handle);
    handle = null;
  };
}

// featureOn reads a named feature from the server's /api/v1/info list. Anything that is not a list of names reads as off.
export function featureOn(features: unknown, name: string): boolean {
  return Array.isArray(features) && features.includes(name);
}

// CONVERT_FEATURE is the name the server gives its preview and export feature in /api/v1/info.
export const CONVERT_FEATURE = "office_convert";

// officeConvert is true while the server offers previews and exports (the feature is in /api/v1/info). It is false until the
// start-up reads the info, and false when the feature is off, so no button is shown for it.
export const officeConvert = writable(false);
