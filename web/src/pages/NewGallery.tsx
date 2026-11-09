// SPDX-License-Identifier: AGPL-3.0-or-later

// The template gallery (nt2, decision 108): a full page of the templates a new document may start from, in the folder or
// directory New was opened from. Blank first, then the built-ins of the kind asked, by category, then the account's own
// templates folder. A click makes the file in that place (the server copies the template), then opens it the way New's
// blank document opens (openNewDocumentTab, decision 102). A viewer or commenter gets a note and a way back, and no template.
import { useEffect, useState, type CSSProperties } from "react";
import { Alert, App, Button, Skeleton, Tooltip, theme } from "antd";
import { ArrowLeft, FilePlus, FileText } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { useLang, useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { session } from "../lib/session";
import { myFolders } from "../lib/files/api";
import { locate, type Here } from "../lib/files/location";
import { parseNewPath, type NewGalleryKind as NewKind } from "../lib/files/newRoute";
import { filesURL } from "../lib/files/route";
import { atLeast } from "../lib/files/roles";
import { coarsePointer } from "../lib/files/editor";
import { fileErrorText } from "../lib/files/fileErrors";
import { openNewDocumentTab, type DocumentKind } from "../lib/files/documents";
import { listAllTemplates, templateThumbURL, type Template } from "../lib/files/templates";
import { refreshTree } from "../lib/query";
import NotFoundPage from "./NotFoundPage";

// The sections, in their order. A section shows the built-ins of its category, for the kind it belongs to.
const SECTIONS: Array<{ key: string; kind: NewKind; label: string }> = [
  { key: "letters", kind: "document", label: "files.gallery.letters" },
  { key: "resumes", kind: "document", label: "files.gallery.resumes" },
  { key: "reports", kind: "document", label: "files.gallery.reports" },
  { key: "workbooks", kind: "spreadsheet", label: "files.gallery.workbooks" },
  { key: "presentations", kind: "presentation", label: "files.gallery.presentations" },
  { key: "designs", kind: "presentation", label: "files.gallery.designs" },
];

// DOC_KIND maps a gallery kind to the blank document New makes for it (the same kinds New's blank documents use).
const DOC_KIND: Record<NewKind, DocumentKind> = { document: "docx", spreadsheet: "xlsx", presentation: "pptx" };

// columns is how many tiles across: 2 on a phone, 3 from 720 px, 4 from 1100 px. Read from the media queries, so no style sheet
// is needed (the page's CSP is nonce-based).
function useColumns(): number {
  const mq = (w: number) => (typeof window !== "undefined" && typeof window.matchMedia === "function" ? window.matchMedia(`(min-width: ${w}px)`) : null);
  const read = () => (mq(1100)?.matches ? 4 : mq(720)?.matches ? 3 : 2);
  const [cols, setCols] = useState(read);
  useEffect(() => {
    const a = mq(720);
    const b = mq(1100);
    if (!a || !b) return;
    const f = () => setCols(read());
    a.addEventListener("change", f);
    b.addEventListener("change", f);
    return () => {
      a.removeEventListener("change", f);
      b.removeEventListener("change", f);
    };
    // read and mq are pure reads of the window; the listeners are set once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return cols;
}

export default function NewGallery() {
  const { t } = useT();
  const lang = useLang();
  const loc = useLocation();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const cols = useColumns();
  const impersonating = !!useStore(session)?.impersonation;
  const route = parseNewPath(loc.pathname, loc.search);
  const folder = route?.folder ?? "";
  const dir = route?.dir ?? null;

  // The place is read from the address: the folder's root or the directory, and what the caller may do there.
  const placeQ = useQuery({
    queryKey: ["newGallery", "place", folder, dir ?? ""],
    enabled: route !== null,
    queryFn: async () => locate(folder, dir, await myFolders()),
  });
  const here: Here | null | undefined = placeQ.data;
  const role = here ? (dir ? here.dirRole : here.folderRole) : null;
  // New is open to an editor or a manager of the place (decision 91: the same rule as the New menu's documents).
  const canNew = !impersonating && atLeast(role, "editor");
  const target = dir ?? here?.rootNodeID ?? null;

  const tplQ = useQuery({
    queryKey: ["newGallery", "templates", route?.kind ?? ""],
    enabled: route !== null && here !== undefined && here !== null && canNew,
    queryFn: () => listAllTemplates(route!.kind),
  });

  if (route === null) return <NotFoundPage />;
  if (placeQ.isSuccess && here === null) return <NotFoundPage />;

  const back = filesURL(folder, dir);
  const shellStyle: CSSProperties = { padding: "0 16px 24px", maxWidth: 1100 };
  const tileStyle: CSSProperties = {
    display: "flex",
    flexDirection: "column",
    gap: 8,
    minWidth: 0,
    width: "100%",
    padding: 8,
    font: "inherit",
    textAlign: "start",
    color: token.colorText,
    background: token.colorBgContainer,
    border: `1px solid ${token.colorBorderSecondary}`,
    borderRadius: token.borderRadiusLG,
    cursor: "pointer",
  };
  const pictureStyle: CSSProperties = {
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    aspectRatio: "4 / 3",
    overflow: "hidden",
    borderRadius: token.borderRadius,
    background: token.colorFillQuaternary,
    color: token.colorTextSecondary,
    fontSize: 32,
  };
  const nameStyle: CSSProperties = { overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap", fontSize: 14 };
  const gridStyle: CSSProperties = {
    display: "grid",
    gridTemplateColumns: `repeat(${cols}, minmax(0, 1fr))`,
    gap: 12,
  };

  // make starts a document: from the template, or blank. The file is opened the way New's blank document is (decision 102).
  function make(tpl?: Template) {
    if (!target || !route) return;
    openNewDocumentTab({
      parent: target,
      kind: DOC_KIND[route.kind],
      template: tpl?.id,
      from: back,
      coarse: coarsePointer(),
      goTo: (to) => navigate(to),
      fail: (err) => fileErrorText(err, lang),
      created: () => void refreshTree(qc, [{ folder, dir }]),
      blocked: () => void message.warning(t("files.newDocument.blocked")),
    });
  }

  const header = (
    <div style={{ display: "flex", alignItems: "center", gap: 8, margin: "16px 0" }}>
      <Tooltip title={t("files.gallery.back")}>
        <Button type="text" aria-label={t("files.gallery.back")} icon={<ArrowLeft size={18} strokeWidth={1.75} aria-hidden />} href={back} />
      </Tooltip>
      <h1 style={{ fontSize: 20, fontWeight: 600, margin: 0 }}>{t("files.gallery.title")}</h1>
    </div>
  );

  if (placeQ.isPending) {
    return (
      <div style={shellStyle} data-testid="gallery-root">
        {header}
        <div style={gridStyle}>
          {Array.from({ length: 6 }, (_, i) => (
            <div key={i} data-testid="gallery-skeleton" style={{ ...tileStyle, cursor: "default" }}>
              <Skeleton.Node active style={{ width: "100%", height: "auto", aspectRatio: "4 / 3" }} />
            </div>
          ))}
        </div>
      </div>
    );
  }

  if (placeQ.isError) {
    return (
      <div style={shellStyle} data-testid="gallery-root">
        {header}
        <Alert
          type="error"
          showIcon
          role="alert"
          message={fileErrorText(placeQ.error, lang)}
          action={<Button size="small" onClick={() => void placeQ.refetch()}>{t("files.gallery.retry")}</Button>}
        />
      </div>
    );
  }

  if (!canNew) {
    return (
      <div style={shellStyle} data-testid="gallery-root">
        {header}
        <Alert
          type="info"
          showIcon
          data-testid="gallery-viewer-note"
          message={<span>{t("files.gallery.viewerNote")} <Link to={back}>{t("files.gallery.back")}</Link></span>}
        />
      </div>
    );
  }

  const all = tplQ.data ?? [];
  const kind = route.kind;
  const sections = SECTIONS.filter((s) => s.kind === kind);
  const mine = all.filter((x) => x.source === "account");
  const blankTile = (
    <button type="button" aria-label={t("files.new.blank")} style={tileStyle} onClick={() => make()}>
      <span style={pictureStyle}><FilePlus size={18} strokeWidth={1.75} aria-hidden /></span>
      <span style={nameStyle}>{t("files.new.blank")}</span>
    </button>
  );

  const tile = (x: Template) => (
    <button key={x.id} type="button" data-template={x.id} aria-label={x.name} style={tileStyle} onClick={() => make(x)}>
      <span style={pictureStyle}>
        {x.has_thumbnail ? (
          <img src={templateThumbURL(x.id)} alt={x.name} loading="lazy" style={{ width: "100%", height: "100%", objectFit: "contain" }} />
        ) : (
          <FileText size={18} strokeWidth={1.75} aria-hidden />
        )}
      </span>
      <span style={nameStyle}>{x.name}</span>
    </button>
  );

  return (
    <div style={shellStyle} data-testid="gallery-root">
      {header}
      {tplQ.isPending && (
        <div style={gridStyle}>
          {Array.from({ length: 6 }, (_, i) => (
            <div key={i} data-testid="gallery-skeleton" style={{ ...tileStyle, cursor: "default" }}>
              <Skeleton.Node active style={{ width: "100%", height: "auto", aspectRatio: "4 / 3" }} />
            </div>
          ))}
        </div>
      )}
      {tplQ.isError && (
        <Alert
          type="error"
          showIcon
          role="alert"
          message={fileErrorText(tplQ.error, lang)}
          action={<Button size="small" onClick={() => void tplQ.refetch()}>{t("files.gallery.retry")}</Button>}
          style={{ marginBottom: 16 }}
        />
      )}
      {/* Blank does not need the template list, so it stays when the list fails (the error and its retry are above). */}
      {!tplQ.isPending && (
        <section data-section="blank" style={{ marginBottom: 24 }}>
          <div style={gridStyle}>{blankTile}</div>
        </section>
      )}
      {tplQ.isSuccess && (
        <>
          {sections.map((s) => {
            const items = all.filter((x) => x.source === "builtin" && x.category === s.key);
            if (items.length === 0) return null;
            return (
              <section key={s.key} data-section={s.key} aria-labelledby={`gallery-${s.key}`} style={{ marginBottom: 24 }}>
                <h2 id={`gallery-${s.key}`} style={{ fontSize: 15, fontWeight: 600, margin: "0 0 12px" }}>{t(s.label)}</h2>
                <div style={gridStyle}>{items.map(tile)}</div>
              </section>
            );
          })}
          {mine.length > 0 && (
            <section data-section="mine" aria-labelledby="gallery-mine" style={{ marginBottom: 24 }}>
              <h2 id="gallery-mine" style={{ fontSize: 15, fontWeight: 600, margin: "0 0 12px" }}>{t("files.gallery.myTemplates")}</h2>
              <div style={gridStyle}>{mine.map(tile)}</div>
            </section>
          )}
        </>
      )}
    </div>
  );
}
