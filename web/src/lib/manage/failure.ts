// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from '../apiError';
import { errorMessage } from '../errors';
import { translate, translatePlural, type Lang } from '../i18n';

// failureText is the text for a refused change on the management pages, in lang. A conflict that names its
// reason (details.reason, such as last_owner) gets the reason's own sentence. Everything else is shown by its
// code. The server's English message is never shown.
export function failureText(err: unknown, lang: Lang): string {
  if (err instanceof ApiError && err.code === 'conflict' && typeof err.details.reason === 'string') {
    return translate(lang, `reason.${err.details.reason}`);
  }
  const msg = errorMessage(err);
  return msg.plural !== undefined ? translatePlural(lang, msg.key, msg.plural) : translate(lang, msg.key);
}
