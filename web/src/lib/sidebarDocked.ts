// SPDX-License-Identifier: AGPL-3.0-or-later

import { Grid } from "antd";

// useSidebarDocked is true where the sidebar is shown beside the page (antd's lg breakpoint, 992px), and false where it is
// a drawer. The shell and the pages read it here, so they agree on one width (decision 66: the toolbar's New is hidden
// where the sidebar holds it).
export function useSidebarDocked(): boolean {
  return !!Grid.useBreakpoint().lg;
}
