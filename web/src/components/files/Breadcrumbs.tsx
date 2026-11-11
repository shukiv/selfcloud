// SPDX-License-Identifier: AGPL-3.0-or-later

import { Breadcrumb, Tooltip } from "antd";
import { ChevronRight } from "lucide-react";
import type { MouseEvent } from "react";
import { useT } from "../../lib/i18n";
import { go } from "../../lib/router";
import type { Crumb } from "../../lib/files/breadcrumbs";

// Place is one stop on the way down: where it leads, and what it is called.
export interface Place {
  key: string;
  label: string;
  href: string;
  // drop says this place takes items dropped on it: the folder, the directory in it (null for the root), and the caller's role there.
  drop?: { folder: string; dir: string | null; role: string | undefined };
}

// dropAttrs are the attributes the page's drag gesture reads from a place that takes a drop (see useGestures).
function dropAttrs(place: Place) {
  const d = place.drop;
  if (!d) return {};
  return { "data-drop-folder": d.folder, "data-drop-dir": d.dir ?? "", "data-drop-role": d.role ?? "", "data-drop-name": place.label };
}

// openPlace follows an in-app link without a page load; a modified click keeps the browser's own behaviour.
function openPlace(e: MouseEvent, href: string) {
  if (e.metaKey || e.ctrlKey || e.shiftKey) return;
  e.preventDefault();
  go(href);
}

// Breadcrumbs shows the places from the top down to here. A long path collapses its middle into one menu.
export default function Breadcrumbs({ crumbs }: { crumbs: Crumb<Place>[] }) {
  const { t } = useT();
  const items = crumbs.map((crumb, i) => {
    const last = i === crumbs.length - 1;
    if (crumb.kind === "above") {
      // The mark for the folders above a trail cut at the 64 nearest (decision 140). It leads nowhere and takes no drop: the
      // folders it stands for are not known here. A screen reader reads its name; the dots are for the eye, with the name as
      // a tooltip.
      const name = t("files.crumbs.above");
      return {
        key: "above",
        title: (
          <Tooltip title={name}>
            <span data-testid="crumbs-above">
              <span aria-hidden="true">…</span>
              <span className="sr-only">{name}</span>
            </span>
          </Tooltip>
        ),
      };
    }
    if (crumb.kind === "gap") {
      return {
        key: `gap-${i}`,
        title: <span aria-label={t("files.crumbs.more")}>…</span>,
        menu: {
          items: crumb.hidden.map((p) => ({
            key: p.key,
            label: <bdi>{p.label}</bdi>,
            onClick: () => go(p.href),
          })),
        },
      };
    }
    const place = crumb.item;
    return {
      key: place.key,
      title: last ? (
        <span aria-current="location" {...dropAttrs(place)}>
          <bdi>{place.label}</bdi>
        </span>
      ) : (
        <a href={place.href} onClick={(e) => openPlace(e, place.href)} {...dropAttrs(place)}>
          <bdi>{place.label}</bdi>
        </a>
      ),
    };
  });
  return (
    <nav aria-label={t("files.crumbs")}>
      <Breadcrumb items={items} separator={<ChevronRight className="icon-dir" size={14} aria-hidden="true" />} />
    </nav>
  );
}
