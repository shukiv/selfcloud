// SPDX-License-Identifier: AGPL-3.0-or-later

import { useLang, useT } from "../../lib/i18n";
import { formatDate } from "../../lib/files/format";

// LastSignIn is a person's Last sign-in cell (decision 114): the date of the person's own last sign-in, or Never.
export default function LastSignIn({ at }: { at: string | null | undefined }) {
  const { t } = useT();
  const lang = useLang();
  return at ? <time dateTime={at}>{formatDate(at, lang)}</time> : <span>{t("keys.expires.never")}</span>;
}
