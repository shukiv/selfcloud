// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from '../apiError';
import { errorMessage } from '../errors';
import { translate, translatePlural, type Lang } from '../i18n';

// failureText is the text for a refused change on the management pages, in lang. A conflict that names its
// reason (details.reason, such as last_owner) gets the reason's own sentence. A reason with no sentence, and every other
// error, is shown by its code. The server's English message is never shown.
export function failureText(err: unknown, lang: Lang): string {
  if (err instanceof ApiError && err.code === 'conflict' && typeof err.details.reason === 'string') {
    // An unknown key comes back as the key itself: then the reason has no sentence, and the generic message is shown.
    const sentence = translate(lang, `reason.${err.details.reason}`);
    if (sentence !== `reason.${err.details.reason}`) return sentence;
  }
  const msg = errorMessage(err);
  return msg.plural !== undefined ? translatePlural(lang, msg.key, msg.plural) : translate(lang, msg.key);
}
