// SPDX-License-Identifier: AGPL-3.0-or-later

import { useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { passwordMinLength } from "../lib/brand";
import Field, { describedBy } from "./Field";
import PasswordField from "./PasswordField";

// NewPasswordFields is the new password and its confirmation, with the rules and whether each is met.
export default function NewPasswordFields({
  idPrefix,
  password,
  confirm,
  onPassword,
  onConfirm,
  labels,
  passwordError = null,
  confirmError = null,
}: {
  idPrefix: string;
  password: string;
  confirm: string;
  onPassword: (v: string) => void;
  onConfirm: (v: string) => void;
  labels: { password: string; confirm: string };
  passwordError?: string | null;
  confirmError?: string | null;
}) {
  const { t } = useT();
  const min = useStore(passwordMinLength);
  const lengthMet = [...password].length >= min;
  const matchMet = password.length > 0 && password === confirm;
  const status = (met: boolean) => (met ? t("newPassword.status.met") : t("newPassword.status.todo"));
  return (
    <>
      <Field id={`${idPrefix}-password`} label={labels.password} error={passwordError}>
        <PasswordField
          id={`${idPrefix}-password`}
          value={password}
          onChange={onPassword}
          invalid={!!passwordError}
          describedBy={describedBy(`${idPrefix}-password`, null, passwordError)}
        />
      </Field>
      <Field id={`${idPrefix}-confirm`} label={labels.confirm} error={confirmError}>
        <PasswordField
          id={`${idPrefix}-confirm`}
          value={confirm}
          onChange={onConfirm}
          invalid={!!confirmError}
          describedBy={describedBy(`${idPrefix}-confirm`, null, confirmError)}
        />
      </Field>
      <section aria-label={t("newPassword.rules")} style={{ marginBottom: 16 }}>
        <p style={{ margin: "0 0 4px", fontWeight: 600 }}>{t("newPassword.rules")}</p>
        <ul style={{ margin: 0, paddingInlineStart: 20 }}>
          <li data-met={lengthMet}>
            {t("newPassword.rule.length", { min })}: <span>{status(lengthMet)}</span>
          </li>
          <li data-met={matchMet}>
            {t("newPassword.rule.match")}: <span>{status(matchMet)}</span>
          </li>
        </ul>
      </section>
    </>
  );
}
