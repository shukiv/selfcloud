// SPDX-License-Identifier: AGPL-3.0-or-later

import { Input } from "antd";
import { Eye, EyeOff } from "lucide-react";
import { useT } from "../lib/i18n";

// A password input with a show and hide control. The value is the raw string.
export default function PasswordField({
  id,
  value,
  onChange,
  autoComplete = "new-password",
  invalid = false,
  describedBy,
}: {
  id: string;
  value: string;
  onChange: (value: string) => void;
  autoComplete?: string;
  invalid?: boolean;
  describedBy?: string;
}) {
  const { t } = useT();
  return (
    <Input.Password
      id={id}
      value={value}
      autoComplete={autoComplete}
      status={invalid ? "error" : undefined}
      aria-invalid={invalid || undefined}
      aria-describedby={describedBy}
      onChange={(e) => onChange(e.target.value)}
      iconRender={(visible) => (
        <span aria-label={visible ? t("password.hide") : t("password.show")} role="img">
          {visible ? <EyeOff size={18} strokeWidth={1.75} /> : <Eye size={18} strokeWidth={1.75} />}
        </span>
      )}
    />
  );
}
