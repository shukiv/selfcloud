// SPDX-License-Identifier: AGPL-3.0-or-later

// Templates (decision 105, nt2): the listing the gallery reads, the picture of a built-in, the copy a click makes, and the
// account's templates folder setting. The paths are the server's; the ID in a thumbnail's path is encoded, since a built-in's
// ID has a colon (builtin:letter).
import { api } from '../api';

// TemplateKind is the kind of template the listing asks for (the server's ?kind=).
export type TemplateKind = 'document' | 'spreadsheet' | 'presentation';

// Template is one template in the listing. A built-in has source "builtin" and a category; an account file has source
// "account", its node ID as its ID, and no category.
export interface Template {
  id: string;
  name: string;
  kind: TemplateKind;
  source: 'builtin' | 'account';
  category?: string;
  node_id?: string;
  has_thumbnail?: boolean;
}

interface TemplatePage {
  items: Template[];
  next_cursor: string;
}

// listTemplatesPage reads one page of the listing for kind. after is the cursor of the page before.
export function listTemplatesPage(kind: TemplateKind, after?: string): Promise<TemplatePage> {
  const q = new URLSearchParams({ kind });
  if (after) q.set('after', after);
  return api<TemplatePage>('GET', `/api/v1/templates?${q.toString()}`);
}

// listAllTemplates reads every page of the listing for kind, in order.
export async function listAllTemplates(kind: TemplateKind): Promise<Template[]> {
  const out: Template[] = [];
  let after: string | undefined;
  for (;;) {
    const page = await listTemplatesPage(kind, after);
    out.push(...page.items);
    if (!page.next_cursor) return out;
    after = page.next_cursor;
  }
}

// templateThumbURL is the picture of a built-in: the same-origin path, with the ID as one encoded path segment.
export function templateThumbURL(id: string): string {
  return `/api/v1/templates/${encodeURIComponent(id)}/thumbnail`;
}

// createFromTemplate makes a document from a template in the directory nod (a node ID; the folder's root node for its root).
// The server copies the template and names the copy; it answers the new node.
export function createFromTemplate(nod: string, template: string): Promise<{ id: string; name: string }> {
  return api<{ id: string; name: string }>('POST', `/api/v1/nodes/${nod}/documents`, { body: { template } });
}

// getTemplatesFolder reads the account's templates folder: its node ID, or null when none is named.
export async function getTemplatesFolder(account: string): Promise<string | null> {
  const r = await api<{ node_id: string | null }>('GET', `/api/v1/accounts/${account}/templates`);
  return r.node_id ?? null;
}

// setTemplatesFolder names the account's templates folder (a directory's node ID), or clears it with null.
export function setTemplatesFolder(account: string, nod: string | null): Promise<{ node_id: string | null }> {
  return api<{ node_id: string | null }>('PUT', `/api/v1/accounts/${account}/templates`, { body: { node_id: nod } });
}
