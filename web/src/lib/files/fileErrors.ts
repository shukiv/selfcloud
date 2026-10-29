// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from '../api';
import { errorMessage } from '../errors';
import { translate, translatePlural, type Lang } from '../i18n';
import { formatBytes } from './format';

// fileErrorText is the text shown for an error from the file API, in lang. It is chosen by the error's
// code; the server's English message is never shown. Codes with no text of their own use the shared
// mapping in errors.ts.
export function fileErrorText(err: unknown, lang: Lang): string {
  if (!(err instanceof ApiError)) return translate(lang, 'error.generic');
  const key = FILE_KEYS[err.code];
  if (key) return translate(lang, key);
  if (err.code === 'quota_exceeded' || err.code === 'storage_full') {
    return quotaText(err, lang);
  }
  const msg = errorMessage(err);
  return msg.plural !== undefined
    ? translatePlural(lang, msg.key, msg.plural)
    : translate(lang, msg.key);
}

const FILE_KEYS: Record<string, string> = {
  invalid_name: 'files.err.invalidName',
  invalid_path: 'files.err.invalidPath',
  download_restricted: 'files.err.downloadRestricted',
  precondition_failed: 'files.err.changed',
  precondition_required: 'files.err.changed',
  upload_expired: 'files.err.uploadExpired',
  checksum_mismatch: 'files.err.checksum',
  chunk_too_large: 'files.err.chunkTooLarge',
  locked: 'files.err.locked',
  too_large: 'files.err.tooLarge',
  too_many_uploads: 'files.err.tooManyUploads',
  range_not_satisfiable: 'files.err.range',
  not_found: 'files.err.notFound',
  forbidden: 'files.err.forbidden',
  conflict: 'files.err.conflict',
};

// quotaText names the scope that is full and the numbers: the space left and the space the request needs.
function quotaText(err: ApiError, lang: Lang): string {
  const scope = typeof err.details.scope === 'string' ? err.details.scope : 'server';
  const scopeKey = SCOPES.includes(scope) ? `files.scope.${scope}` : 'files.scope.server';
  const available = Number(err.details.available_bytes ?? 0);
  const required = Number(err.details.required_bytes ?? 0);
  const key = err.code === 'quota_exceeded' ? 'files.err.quota' : 'files.err.storage';
  return translate(lang, key, {
    scope: translate(lang, scopeKey),
    available: formatBytes(Math.max(0, available), lang),
    required: formatBytes(Math.max(0, required), lang),
  });
}

const SCOPES = ['quota', 'account', 'account_reservations', 'server'];
