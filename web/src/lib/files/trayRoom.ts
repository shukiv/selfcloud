// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from "../store";

// trayRoom is how many pixels at the bottom of the page the upload tray covers, with a gap. The file browser leaves that
// room under its last rows, so a person can scroll the rows clear of the tray. Zero when no tray is shown.
export const trayRoom = writable(0);
