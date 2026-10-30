// SPDX-License-Identifier: AGPL-3.0-or-later

import { Alert as AntdAlert } from "antd";
import type { Message } from "../lib/errors";
import { useT } from "../lib/i18n";

// Alert shows a catalogue message. Errors are announced at once; a status notice politely.
export default function Alert({ message, tone = "error" }: { message: Message | null; tone?: "error" | "success" }) {
  const { t, tp } = useT();
  if (!message) return null;
  const text = message.plural !== undefined ? tp(message.key, message.plural) : t(message.key);
  return <AntdAlert type={tone === "error" ? "error" : "success"} showIcon role={tone === "error" ? "alert" : "status"} message={text} />;
}
