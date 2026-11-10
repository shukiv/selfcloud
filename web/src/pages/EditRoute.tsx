// SPDX-License-Identifier: AGPL-3.0-or-later

// The /edit/<node> address, for both editors (design-ce 3.1, decision 124). The node is read once: a code file goes to the code
// editor, and everything else to the office editor, as before (which also answers for a folder or a type it does not open). A
// file the read cannot reach goes to the office editor too, which shows its own failure. This module is lazy, and it imports
// neither editor's module until it has decided, so the first load does not change.
import { lazy, Suspense, useEffect, useState } from "react";
import { useLocation } from "react-router-dom";
import { Spin } from "antd";
import { needsSniff } from "../lib/code/kind";
import { statNode } from "../lib/files/api";
import type { Node } from "../lib/files/types";
import { useT } from "../lib/i18n";

const CodeEditPage = lazy(() => import("./CodeEditPage"));
const OfficeEditPage = lazy(() => import("./OfficeEditPage"));

// isCodeFile says a node is opened by the code editor: a file the rules of lib/code/kind.ts may take as code or plain text (an
// office file, a picture and a folder are not). Whether its bytes are text is the editor's own check.
export function isCodeFile(n: Node): boolean {
  return n.kind === "file" && needsSniff(n);
}

export default function EditRoute() {
  const { t } = useT();
  const nod = /^\/edit\/([^/]+)$/.exec(useLocation().pathname)?.[1] ?? "";
  const [kind, setKind] = useState<"reading" | "code" | "office">("reading");

  useEffect(() => {
    let live = true;
    setKind("reading");
    statNode(nod).then(
      (n) => {
        if (live) setKind(isCodeFile(n) ? "code" : "office");
      },
      () => {
        if (live) setKind("office");
      },
    );
    return () => {
      live = false;
    };
  }, [nod]);

  if (kind === "reading") {
    return (
      <div role="status" style={{ display: "grid", placeItems: "center", padding: 24 }}>
        <Spin tip={t("files.editor.opening")} />
      </div>
    );
  }
  return <Suspense fallback={<Spin />}>{kind === "code" ? <CodeEditPage /> : <OfficeEditPage />}</Suspense>;
}
