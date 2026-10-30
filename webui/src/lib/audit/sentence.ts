// SPDX-License-Identifier: AGPL-3.0-or-later

// Audit entries as plain sentences. The server gives an actor as {kind, id} and a target as an ID, plus a
// few details (a name, a purpose). A sentence is a catalogue key with its parameters. A parameter is either
// text (a name from the page's lookup) or a catalogue key (a generic word such as "Someone"), so this module
// stays free of the language and the page translates the result.

export interface AuditActor {
  kind: string;
  id: string;
}

export interface AuditEntry {
  action: string;
  actor: AuditActor;
  target: string;
  details?: Record<string, unknown>;
}

// Ref is one parameter of a sentence: literal text, or a catalogue key to translate.
export type Ref = { text: string } | { key: string };

export interface Sentence {
  key: string;
  params: Record<string, Ref>;
}

// Names are looked up by ID. A name the page has not loaded is undefined, and a generic word is used.
export interface Names {
  person(id: string): string | undefined;
  space(id: string): string | undefined;
}

function detail(d: Record<string, unknown> | undefined, field: string): string {
  const v = d?.[field];
  return typeof v === 'string' ? v : '';
}

// actorRef says who acted: a person's name when the lookup has it, else a word for the kind of actor.
export function actorRef(actor: AuditActor, names: Names): Ref {
  if (actor.kind === 'user') {
    const name = names.person(actor.id);
    return name !== undefined ? { text: name } : { key: 'audit.someone' };
  }
  switch (actor.kind) {
    case 'account_key':
      return { key: 'audit.apiKey' };
    case 'admin_key':
      return { key: 'audit.adminKey' };
    case 'cli':
      return { key: 'audit.cli' };
    case 'system':
      return { key: 'audit.system' };
    default:
      return { key: 'audit.someone' };
  }
}

// sentence builds the plain sentence for an entry. Each action has a sentence of its own, with the catalogue key
// audit.<action>; any other action reads as audit.unlisted, and the page shows the raw action beside it. A parameter
// is filled only where the action's target really is that thing: a grant's or a group's target is not a person
// or a team space, so those sentences name nothing they cannot know.
export function sentence(entry: AuditEntry, names: Names): Sentence {
  const d = entry.details;
  const actor = actorRef(entry.actor, names);
  const person = names.person(entry.target);
  const target: Ref = person !== undefined ? { text: person } : { key: 'audit.aPerson' };
  const spaceName = detail(d, 'name') || names.space(entry.target) || '';
  const space: Ref = spaceName !== '' ? { text: spaceName } : { key: 'audit.aSpace' };
  const groupName = detail(d, 'name');
  const group: Ref = groupName !== '' ? { text: groupName } : { key: 'audit.aGroup' };
  const tenantName = detail(d, 'name');
  const tenant: Ref = tenantName !== '' ? { text: tenantName } : { key: 'audit.aTenant' };
  switch (entry.action) {
    case 'user.create':
      return {
        key: detail(d, 'role') === 'owner' ? 'audit.user.createOwner' : 'audit.user.create',
        params: { actor, target },
      };
    case 'verification.send':
      return {
        key: detail(d, 'purpose') === 'reset' ? 'audit.verification.reset' : 'audit.verification.send',
        params: { actor, target },
      };
    case 'user.verify':
      return { key: 'audit.user.verify', params: { target } };
    case 'folder.create':
    case 'folder.update':
    case 'folder.delete':
    case 'folder.settings':
      return { key: `audit.${entry.action}`, params: { actor, name: space } };
    case 'group.create':
    case 'group.update':
    case 'group.delete':
      return { key: `audit.${entry.action}`, params: { actor, name: group } };
    case 'account.create':
      return { key: 'audit.account.create', params: { actor, name: tenant } };
    case 'user.update':
    case 'user.delete':
    case 'user.password_reset':
    case 'user.password_change':
    case 'admin.create':
    case 'admin.delete':
      return { key: `audit.${entry.action}`, params: { actor, target } };
    case 'permission.set':
    case 'permission.remove':
    case 'permission.expired':
    case 'node.trash':
    case 'node.restore':
    case 'node.purge':
    case 'node.sharing':
    case 'node.transfer':
    case 'access.request':
    case 'grant.set':
    case 'grant.remove':
    case 'group.member.add':
    case 'group.member.remove':
    case 'key.create':
    case 'key.revoke':
    case 'account.update':
    case 'account.delete':
    case 'account.deleted':
    case 'tokens.revoke':
    case 'token.mint':
      return { key: `audit.${entry.action}`, params: { actor } };
    default:
      return { key: 'audit.unlisted', params: { actor } };
  }
}

// isolate wraps text in directional isolate marks, so a name in the other direction keeps its order inside a
// sentence of this direction.
export function isolate(text: string): string {
  return String.fromCharCode(0x2068) + text + String.fromCharCode(0x2069);
}

// renderSentence turns a sentence into text. translate looks a catalogue key up with parameters, so the same
// function serves any language; text parameters are isolated, and key parameters are translated first.
export function renderSentence(s: Sentence, translate: (key: string, params?: Record<string, string>) => string): string {
  const params: Record<string, string> = {};
  for (const [name, ref] of Object.entries(s.params)) {
    params[name] = 'text' in ref ? isolate(ref.text) : translate(ref.key);
  }
  return translate(s.key, params);
}
