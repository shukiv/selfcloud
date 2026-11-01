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
  // via says that an installation admin acted while signed in as the actor (decision 41). It is added after the sentence.
  via?: { key: string; params: Record<string, Ref> };
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

// actorRef says who acted: a person's name when the lookup has it, else a word for the kind of actor. An installation
// admin is not a person of any account, so the lookup never has it: the server marks such a row with actor_admin and
// gives the admin's name in actor_name, and a removed admin has no name, which reads as "an installation admin".
export function actorRef(actor: AuditActor, names: Names, details?: Record<string, unknown>): Ref {
  if (actor.kind === 'user') {
    const name = names.person(actor.id);
    if (name !== undefined) return { text: name };
    if (details?.actor_admin === true) {
      const admin = detail(details, 'actor_name');
      return admin !== '' ? { text: admin } : { key: 'audit.anAdmin' };
    }
    return { key: 'audit.someone' };
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
  const s = baseSentence(entry, names);
  const by = entry.details?.impersonated_by;
  if (typeof by === 'string' && by !== '') {
    const admin = detail(entry.details, 'impersonated_by_name');
    s.via = {
      key: 'audit.viaAdmin',
      params: { admin: admin !== '' ? { text: admin } : { key: 'audit.anAdmin' } },
    };
  }
  return s;
}

function baseSentence(entry: AuditEntry, names: Names): Sentence {
  const d = entry.details;
  const actor = actorRef(entry.actor, names, d);
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
      if (detail(d, 'purpose') === 'reset') {
        return {
          key: detail(d, 'method') === 'link' ? 'audit.verification.resetLink' : 'audit.verification.reset',
          params: { actor, target },
        };
      }
      return { key: 'audit.verification.send', params: { actor, target } };
    case 'impersonation.start':
      return { key: 'audit.impersonation.start', params: { actor, target } };
    case 'impersonation.end':
      return { key: `audit.impersonation.end.${endReason(detail(d, 'reason'))}`, params: { actor, target } };
    case 'admin.password_refused':
      return { key: `audit.admin.password_refused.${wayName(detail(d, 'way'))}`, params: { actor, target } };
    case 'user.password_set':
      return { key: 'audit.user.password_set', params: { actor, target } };
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

// endReason groups the reasons an impersonation ends for the viewers. The reasons come from the server (decision 41).
function endReason(reason: string): string {
  switch (reason) {
    case 'returned':
      return 'returned';
    case 'expired':
      return 'expired';
    case 'admin_signed_out':
      return 'adminSignedOut';
    case 'admin_password_change':
    case 'admin_session_ended':
    case 'admin_sessions_ended':
    case 'admin_removed':
      return 'adminEnded';
    case 'disabled':
    case 'deleted':
    case 'password_set':
    case 'password_reset':
    case 'password_change':
      return 'accountChanged';
    default:
      return 'other';
  }
}

// wayName names the way a refused attempt tried (decision 41).
function wayName(way: string): string {
  switch (way) {
    case 'sign_in_as':
      return 'signInAs';
    case 'typed_password':
      return 'typedPassword';
    case 'reset_link':
      return 'resetLink';
    default:
      return 'other';
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
  const main = translate(s.key, refParams(s.params, translate));
  if (!s.via) return main;
  return `${main} ${translate(s.via.key, refParams(s.via.params, translate))}`;
}

function refParams(refs: Record<string, Ref>, translate: (key: string, params?: Record<string, string>) => string): Record<string, string> {
  const params: Record<string, string> = {};
  for (const [name, ref] of Object.entries(refs)) {
    params[name] = 'text' in ref ? isolate(ref.text) : translate(ref.key);
  }
  return params;
}
