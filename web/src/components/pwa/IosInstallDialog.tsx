// SPDX-License-Identifier: AGPL-3.0-or-later

// The instructions for adding the app to the home screen on iPhone and iPad, where Safari has no install prompt (decision 65).

import { Button, Modal, Typography } from "antd";
import { Plus, Share } from "lucide-react";
import { useT } from "../../lib/i18n";
import { useCoarsePointer } from "../../lib/pointer";

// The steps are the same in every iOS browser: Chrome and Firefox on iOS share the system's share sheet, so the words name the
// browser's own Share button, not Safari's.
export default function IosInstallDialog({ onClose }: { onClose: () => void }) {
  const { t } = useT();
  const coarse = useCoarsePointer();
  return (
    <Modal
      open
      title={t("pwa.iosTitle")}
      onCancel={onClose}
      maskClosable={false}
      destroyOnClose
      footer={
        <Button type="primary" onClick={onClose} style={coarse ? { minHeight: 44, minWidth: 44 } : undefined}>
          {t("pwa.close")}
        </Button>
      }
    >
      <Typography.Paragraph>{t("pwa.iosIntro")}</Typography.Paragraph>
      <ol style={{ paddingInlineStart: 20, margin: 0 }}>
        <li style={{ marginBottom: 12 }}>
          <Share size={16} aria-hidden style={{ verticalAlign: "text-bottom", marginInlineEnd: 6 }} />
          {t("pwa.iosStep1")}
        </li>
        <li style={{ marginBottom: 12 }}>
          <Plus size={16} aria-hidden style={{ verticalAlign: "text-bottom", marginInlineEnd: 6 }} />
          {t("pwa.iosStep2")}
        </li>
        <li>{t("pwa.iosStep3")}</li>
      </ol>
    </Modal>
  );
}
