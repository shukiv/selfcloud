// SPDX-License-Identifier: AGPL-3.0-or-later

// The played list (mp4): the media files the signed-in person stopped at, the latest place first. A thin call through
// api(), as the search call is; the server keeps one place per person and file, and leaves out what the person no
// longer reaches.
import { api } from "../api";
import type { Node, Page } from "../files/types";

// PlayedItem is a played media file with the place the person stopped at, in seconds (the length 0 when unknown), and
// the time they stopped there.
export interface PlayedItem extends Node {
  position_seconds: number;
  duration_seconds: number;
  played_at: string;
}

// listPlayed reads one page of the played list. The params carry the limit and the cursor, as the other lists do.
export function listPlayed(params: URLSearchParams): Promise<Page<PlayedItem>> {
  return api<Page<PlayedItem>>("GET", `/api/v1/media/played?${params.toString()}`);
}
