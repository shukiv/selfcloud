// SPDX-License-Identifier: AGPL-3.0-or-later

// The search call. It is a thin call through api(); the server checks every rule, and the page checks the words first.
import { api } from "../api";
import type { Node, Page } from "../files/types";

// Snippet is a content hit's text, in search form: lower case, marks removed, as the server indexes it. Ranges are [start, end)
// byte offsets into text. It is plain text: the page never reads it as markup.
export interface Snippet {
  text: string;
  ranges: [number, number][];
}

export interface SearchHit extends Node {
  snippet?: Snippet;
}

export function searchNodes(params: URLSearchParams): Promise<Page<SearchHit>> {
  return api<Page<SearchHit>>("GET", `/api/v1/search?${params.toString()}`);
}
