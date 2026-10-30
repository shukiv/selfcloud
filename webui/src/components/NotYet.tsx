// SPDX-License-Identifier: AGPL-3.0-or-later

import { Result } from "antd";
import { useT } from "../lib/i18n";
import { useStore } from "../lib/store";
import { brand } from "../lib/brand";

// NotYet stands in for a page the React interface has not built yet. The part that builds it
// replaces the route; until then the address still reaches the shell and its menu.
export default function NotYet() {
  const { t } = useT();
  const product = useStore(brand).name;
  return <Result status="info" title={t("placeholder.comingSoon")} subTitle={t("placeholder.body", { product })} />;
}
