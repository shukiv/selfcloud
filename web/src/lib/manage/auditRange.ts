// SPDX-License-Identifier: AGPL-3.0-or-later

import { auditExportURL, type AuditExportFilter } from './api';

const DAY = /^(\d{4})-(\d{2})-(\d{2})$/;

// dayEdge turns a date input's YYYY-MM-DD into the UTC instant of the day's start or of the next day's start. The
// browser reads the date in the viewer's zone, so a day around a daylight-saving change is covered whole: 23 or 25
// hours, not always 24.
function dayEdge(date: string, end: boolean): string | undefined {
  const m = DAY.exec(date);
  if (m === null) return undefined;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]) + (end ? 1 : 0)).toISOString();
}

export function dateStart(date: string): string | undefined {
  return dayEdge(date, false);
}

export function dateEnd(date: string): string | undefined {
  return dayEdge(date, true);
}

export interface AuditRange {
  since?: string;
  until?: string;
}

// auditRange is the query filter for the two date inputs: since is the From day's first instant, until is the instant
// after the To day, and an empty input is left out.
export function auditRange(from: string, to: string): AuditRange {
  const since = dateStart(from);
  const until = dateEnd(to);
  return { ...(since === undefined ? {} : { since }), ...(until === undefined ? {} : { until }) };
}

// rangeInvalid says the two dates contradict each other. A single date, or the same date twice, is a valid range.
export function rangeInvalid(from: string, to: string): boolean {
  return from !== '' && to !== '' && to < from;
}

// downloadAuditExport starts the CSV download through the browser's own link, so the session cookie travels and the
// page never fetches the file into memory.
export function downloadAuditExport(account: string, filter: AuditExportFilter): void {
  const a = document.createElement('a');
  a.href = auditExportURL(account, filter);
  a.setAttribute('download', '');
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
}
