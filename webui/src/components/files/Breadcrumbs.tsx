// SPDX-License-Identifier: AGPL-3.0-or-later

import { Breadcrumb } from "antd";
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
        <span aria-current="location">
          <bdi>{place.label}</bdi>
        </span>
      ) : (
        <a href={place.href} onClick={(e) => openPlace(e, place.href)}>
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
