// SPDX-License-Identifier: AGPL-3.0-or-later

import { useId, useRef, useState, type KeyboardEvent } from "react";
import { Alert, Button, Input, Space } from "antd";
import type { TextAreaRef } from "antd/es/input/TextArea";
import { useQueryClient } from "@tanstack/react-query";
import { createComment } from "../../lib/comments/api";
import { useCT } from "../../lib/comments/i18n";
import {
  charCount,
  checkBody,
  commentFault,
  insertMention,
  mentionRefs,
  MAX_CHARS,
  type Chosen,
} from "../../lib/comments/logic";
import { sessionChanged } from "../../lib/session";
import { faultMessage } from "./faults";
import { refreshAfterChange } from "./refresh";
import MentionPicker from "./MentionPicker";

// Composer writes a comment on an item, or a reply under a top-level comment. The text stays as typed when a send fails:
// it is cleared only when the server has taken it, and only if the person has not typed on meanwhile. The box stays
// editable during a send, and a second send is refused while one is in flight. A failed send leaves the focus in the box.
// The text is plain: it is sent as typed, never as HTML.
export default function Composer({
  nodeID,
  parentID,
  label,
  submit,
  onDone,
  onCancel,
  autoFocus = false,
}: {
  nodeID: string;
  parentID?: string;
  label: string;
  submit: string;
  onDone?: () => void;
  onCancel?: () => void;
  autoFocus?: boolean;
}) {
  const { t } = useCT();
  const qc = useQueryClient();
  const id = useId();
  const [text, setText] = useState("");
  const [chosen, setChosen] = useState<Chosen[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const inFlight = useRef(false);
  const area = useRef<TextAreaRef>(null);
  // textNow is the text as the last render drew it, so the end of a send can tell whether the person typed on.
  const textNow = useRef(text);
  textNow.current = text;

  const refs = mentionRefs(text, chosen);
  const count = charCount(text);

  const send = async () => {
    if (inFlight.current) return;
    const broke = checkBody(text, refs);
    if (broke) {
      setError(t(broke.key, broke.params));
      return;
    }
    const sent = text;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    const stale = sessionChanged();
    try {
      await createComment(nodeID, {
        body: sent,
        ...(parentID ? { parent_id: parentID } : {}),
        ...(refs.length > 0 ? { mentions: refs } : {}),
      });
      // A person who signed out meanwhile gets no answer written for the session they left.
      if (stale()) return;
      if (textNow.current === sent) {
        setText("");
        setChosen([]);
        onDone?.();
      }
      await refreshAfterChange(qc, nodeID);
    } catch (err) {
      if (stale()) return;
      setError(faultMessage(err, t, "comments.saveFailed"));
      area.current?.focus();
      // The text is kept on every refusal, so a switch that is off does not reload the panel here: the panel is read
      // again on its next read, and the text is still in the box. A parent that is gone, or a reply under a reply, reads now.
      const fault = commentFault(err);
      if (fault === "gone" || fault === "reply") await refreshAfterChange(qc, nodeID);
    } finally {
      inFlight.current = false;
      setBusy(false);
    }
  };

  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // Ctrl or Command with Enter sends, as the chat-style boxes do.
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      void send();
    }
  };

  // Escape with text in the composer, from the box or from the name picker, closes nothing around it: the drawer would
  // lose the draft. The picker's own Escape closes its list and stops there.
  const onComposerKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Escape" && text.length > 0) e.stopPropagation();
  };

  const pick = (p: { ref: string; name: string }) => {
    setText((cur) => insertMention(cur, p.name));
    setChosen((cur) => (cur.some((c) => c.ref === p.ref) ? cur : [...cur, { ref: p.ref, name: p.name }]));
    // The focus goes back to the box, so the person can go on writing.
    area.current?.focus();
  };

  return (
    <div style={{ display: "grid", gap: 8, marginBlockEnd: 12 }} onKeyDown={onComposerKey}>
      <label htmlFor={`${id}-text`} style={{ fontWeight: 600 }}>
        {label}
      </label>
      <Input.TextArea
        id={`${id}-text`}
        ref={area}
        rows={3}
        value={text}
        autoFocus={autoFocus}
        onChange={(e) => setText(e.target.value)}
        onKeyDown={onKey}
        aria-describedby={`${id}-hint ${id}-count`}
        status={error ? "error" : undefined}
      />
      <p id={`${id}-count`} style={{ margin: 0, fontSize: 13, textAlign: "end" }}>
        {t("comments.counter", { count, max: MAX_CHARS })}
      </p>
      <p id={`${id}-hint`} className="hint" style={{ margin: 0, fontSize: 13 }}>
        {t("comments.hint")}
      </p>
      <MentionPicker nodeID={nodeID} onPick={pick} />
      {error && <Alert type="error" showIcon role="alert" message={error} />}
      <Space wrap>
        <Button type="primary" onClick={() => void send()} loading={busy}>
          {submit}
        </Button>
        {onCancel && <Button onClick={onCancel}>{t("comments.cancel")}</Button>}
      </Space>
    </div>
  );
}
