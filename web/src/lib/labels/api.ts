// SPDX-License-Identifier: AGPL-3.0-or-later

// The label calls. Each is a thin call through api(): the server checks every rule and every value, and these only
// send what the controls offer. A label name and a text value are plain text; the interface never renders them as HTML.
import { api } from '../api';

export type LabelType = 'text' | 'number' | 'date' | 'user' | 'choice';

export interface LabelChoice {
  id: string;
  name: string;
  archived: boolean;
}

// LabelField is a field the account defines. Values is the number of nodes that hold a value; only the owner and the
// account key are sent it.
export interface LabelField {
  id: string;
  name: string;
  type: LabelType;
  restricted: boolean;
  choices: LabelChoice[];
  values?: number;
}

// LabelUser is the member a user value names. Name is absent when the caller may not learn names, or the member has none.
export interface LabelUser {
  id: string;
  name?: string;
}

// NodeLabel is one field's value on a node. Value is null while the field is not set.
export interface NodeLabel {
  field_id: string;
  name: string;
  type: LabelType;
  restricted: boolean;
  value: string | number | LabelUser | LabelChoice | null;
}

export interface LabelFieldBody {
  name: string;
  type: LabelType;
  restricted: boolean;
  choices?: { id?: string; name: string; archived?: boolean }[];
}

export interface LabelFieldPatch {
  name?: string;
  type?: LabelType;
  restricted?: boolean;
  choices?: { id?: string; name: string; archived?: boolean }[];
}

export function listLabelFields(): Promise<{ items: LabelField[] }> {
  return api<{ items: LabelField[] }>('GET', '/api/v1/label-fields');
}

export function createLabelField(account: string, body: LabelFieldBody): Promise<LabelField> {
  return api<LabelField>('POST', `/api/v1/accounts/${account}/label-fields`, { body });
}

export function updateLabelField(account: string, field: string, body: LabelFieldPatch): Promise<LabelField> {
  return api<LabelField>('PATCH', `/api/v1/accounts/${account}/label-fields/${field}`, { body });
}

export function deleteLabelField(account: string, field: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/accounts/${account}/label-fields/${field}`);
}

export function listNodeLabels(node: string): Promise<{ items: NodeLabel[] }> {
  return api<{ items: NodeLabel[] }>('GET', `/api/v1/nodes/${node}/labels`);
}

export function setNodeLabel(node: string, field: string, value: string | number | null): Promise<void> {
  return api<void>('PUT', `/api/v1/nodes/${node}/labels/${field}`, { body: { value } });
}

export function clearNodeLabel(node: string, field: string): Promise<void> {
  return api<void>('DELETE', `/api/v1/nodes/${node}/labels/${field}`);
}
