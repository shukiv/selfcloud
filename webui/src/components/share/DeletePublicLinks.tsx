// SPDX-License-Identifier: AGPL-3.0-or-later

import { sessionChanged } from "../../lib/session";
import { useState } from "react";
import { Alert, Button, Modal } from "antd";
import { useLang, useT } from "../../lib/i18n";
import { failureText } from "../../lib/manage/failure";
import { canDeleteAll, deleteAllCount } from "../../lib/manage/links";
import { deleteAllPublicLinks } from "../../lib/manage/api";

// DeletePublicLinks deletes every link that anyone can open, and every drop link, of an account. It is one deliberate
// action: it asks first, and the confirmation names the count. Links that only people in the account can open are kept.
// The owner's Links page and the installation admin's account page use it (part v4).
export default function DeletePublicLinks({
  account,
  count,
  ondone,
}: {
  account: string;
  count: number;
  ondone: (deleted: number) => void;
}) {
  const { t, tp } = useT();
  const lang = useLang();
  const [asking, setAsking] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const n = deleteAllCount(count);

  const confirmDelete = async () => {
    // A change of person while the delete is out: its answer is not shown to the next person.
    const stale = sessionChanged();
    setBusy(true);
    setError(null);
    try {
      const reply = await deleteAllPublicLinks(account);
      if (stale()) return;
      setAsking(false);
      setNotice(tp("links.deleteAll.done", reply.deleted));
      ondone(reply.deleted);
    } catch (err) {
      if (stale()) return;
      setError(failureText(err, lang));
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {canDeleteAll(count, busy) && (
        <p style={{ margin: "8px 0" }}>
          <Button danger onClick={() => setAsking(true)}>
            {t("links.deleteAll.action")}
          </Button>
        </p>
      )}
      {notice && (
        <p className="hint" role="status" style={{ margin: 0, fontSize: 13 }}>
          {notice}
        </p>
      )}
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      <Modal
        open={asking}
        title={t("links.deleteAll.title")}
        onCancel={() => setAsking(false)}
        maskClosable={false}
        footer={[
          <Button key="cancel" onClick={() => setAsking(false)}>
            {t("common.cancel")}
          </Button>,
          <Button key="go" danger type="primary" loading={busy} disabled={busy} onClick={() => void confirmDelete()}>
            {t("links.deleteAll.action")}
          </Button>,
        ]}
        destroyOnClose
      >
        <p>{tp("links.deleteAll.message", n)}</p>
        {error && <Alert type="error" showIcon role="alert" message={error} />}
      </Modal>
    </>
  );
}
