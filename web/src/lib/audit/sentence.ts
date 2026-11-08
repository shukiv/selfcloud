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
  // outcome is "failed" for a refused attempt (sign-in, sign-in link). The server writes it on the row.
  outcome?: string;
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

// flag reads a yes-or-no detail. It is undefined when the row lacks the field, so the sentence falls back to a generic one.
function flag(d: Record<string, unknown> | undefined, field: string): boolean | undefined {
  const v = d?.[field];
  return typeof v === 'boolean' ? v : undefined;
}

// number reads a count or a percentage. It is undefined when the row lacks the field.
function number(d: Record<string, unknown> | undefined, field: string): number | undefined {
  const v = d?.[field];
  return typeof v === 'number' ? v : undefined;
}

// nestedName reads the name inside a detail that holds an object, such as a move's destination.
function nestedName(d: Record<string, unknown> | undefined, field: string): string {
  const v = d?.[field];
  return v !== null && typeof v === 'object' ? detail(v as Record<string, unknown>, 'name') : '';
}

// orWord is the item's name as text, or the generic word for that kind of item when the row has no name.
function orWord(text: string, key: string): Ref {
  return text !== '' ? { text } : { key };
}

// personOf names the person with the ID a row holds, when the page has them, and a generic word otherwise.
function personOf(names: Names, id: string): Ref {
  const name = id !== '' ? names.person(id) : undefined;
  return name !== undefined ? { text: name } : { key: 'audit.aPerson' };
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
    // Without the admin's name, the sentence says "an installation admin" once, not twice.
    s.via =
      admin !== ''
        ? { key: 'audit.viaAdmin', params: { admin: { text: admin } } }
        : { key: 'audit.viaAnAdmin', params: {} };
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
    // A name the row lacks gets its own sentence, so the noun is not said twice ("the team space a team space").
    case 'folder.create':
      if (spaceName === '') return { key: 'audit.folder.create.plain', params: { actor } };
      return { key: 'audit.folder.create', params: { actor, name: space } };
    case 'folder.update':
      if (spaceName === '') return { key: 'audit.folder.update.plain', params: { actor } };
      return { key: 'audit.folder.update', params: { actor, name: space } };
    case 'folder.delete':
      if (spaceName === '') return { key: 'audit.folder.delete.plain', params: { actor } };
      return { key: 'audit.folder.delete', params: { actor, name: space } };
    case 'folder.settings':
      return { key: 'audit.folder.settings', params: { actor, name: space } };
    case 'group.create':
      if (groupName === '') return { key: 'audit.group.create.plain', params: { actor } };
      return { key: 'audit.group.create', params: { actor, name: group } };
    case 'group.update':
      if (groupName === '') return { key: 'audit.group.update.plain', params: { actor } };
      return { key: 'audit.group.update', params: { actor, name: group } };
    case 'group.delete':
      if (groupName === '') return { key: 'audit.group.delete.plain', params: { actor } };
      return { key: 'audit.group.delete', params: { actor, name: group } };
    case 'account.create':
      if (tenantName === '') return { key: 'audit.account.create.plain', params: { actor } };
      return { key: 'audit.account.create', params: { actor, name: tenant } };
    case 'admin.create':
      if (person === undefined) return { key: 'audit.admin.create.plain', params: { actor } };
      return { key: 'audit.admin.create', params: { actor, target } };
    case 'admin.delete':
      if (person === undefined) return { key: 'audit.admin.delete.plain', params: { actor } };
      return { key: 'audit.admin.delete', params: { actor, target } };
    // A change of address reads as the update it is (decision 89); it needs no sentence of its own.
    case 'user.email_changed':
      return { key: 'audit.user.update', params: { actor, target } };
    case 'user.update':
    case 'user.delete':
    case 'user.password_reset':
    case 'user.password_change':
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
    case 'access.approve':
      return { key: 'audit.access.approve', params: { actor, person: personOf(names, detail(d, 'user')) } };
    case 'access.deny':
      return { key: 'audit.access.deny', params: { actor, person: personOf(names, detail(d, 'user')) } };
    case 'account.comments_policy': {
      const on = flag(d, 'comments');
      const key =
        on === undefined
          ? 'audit.account.comments_policy'
          : on
            ? 'audit.account.comments_policy.on'
            : 'audit.account.comments_policy.off';
      return { key, params: { actor } };
    }
    case 'account.sharing_policy': {
      const on = flag(d, 'public_links');
      const key =
        on === undefined
          ? 'audit.account.sharing_policy'
          : on
            ? 'audit.account.sharing_policy.on'
            : 'audit.account.sharing_policy.off';
      return { key, params: { actor } };
    }
    case 'account.quota_warning': {
      const percent = number(d, 'percent');
      if (percent === undefined) return { key: 'audit.account.quota_warning', params: { actor } };
      return { key: 'audit.account.quota_warning.percent', params: { actor, percent: { text: String(percent) } } };
    }
    case 'account.storage_policy':
      return {
        key: number(d, 'user_quota_bytes') === 0 ? 'audit.account.storage_policy.none' : 'audit.account.storage_policy',
        params: { actor },
      };
    case 'auth.sign_in_link':
      return { key: entry.outcome === 'failed' ? 'audit.auth.sign_in_link.failed' : 'audit.auth.sign_in_link', params: { actor } };
    case 'comment.create':
      return { key: d?.reply === true ? 'audit.comment.create.reply' : 'audit.comment.create', params: { actor } };
    case 'comment.delete':
      return { key: 'audit.comment.delete', params: { actor } };
    case 'comment.edit':
      return { key: 'audit.comment.edit', params: { actor } };
    case 'comment.resolve': {
      const resolved = flag(d, 'resolved');
      const key =
        resolved === undefined
          ? 'audit.comment.resolve'
          : resolved
            ? 'audit.comment.resolve.resolved'
            : 'audit.comment.resolve.reopened';
      return { key, params: { actor } };
    }
    case 'label_field.create':
      return { key: 'audit.label_field.create', params: { actor } };
    case 'label_field.delete':
      return { key: 'audit.label_field.delete', params: { actor } };
    case 'label_field.update':
      return {
        key: number(d, 'values') === undefined ? 'audit.label_field.update' : 'audit.label_field.update.opened',
        params: { actor },
      };
    case 'link.create':
      if (d?.upload_only === true) return { key: 'audit.link.create.drop', params: { actor } };
      if (detail(d, 'audience') === 'anyone') return { key: 'audit.link.create.anyone', params: { actor } };
      return { key: 'audit.link.create', params: { actor } };
    case 'link.delete':
      return { key: d?.upload_only === true ? 'audit.link.delete.drop' : 'audit.link.delete', params: { actor } };
    case 'link.delete_public':
      return { key: 'audit.link.delete_public', params: { actor } };
    case 'link.expired':
      return { key: 'audit.link.expired', params: { actor } };
    case 'link.open':
      return { key: 'audit.link.open', params: { actor } };
    case 'link.reset':
      return { key: 'audit.link.reset', params: { actor } };
    case 'link.update':
      return { key: 'audit.link.update', params: { actor } };
    case 'link.throttled':
      return { key: 'audit.link.throttled', params: {} };
    case 'node.appearance':
      return { key: 'audit.node.appearance', params: { actor } };
    case 'node.create': {
      const name = detail(d, 'name');
      if (detail(d, 'via') === 'share_link') {
        return { key: 'audit.node.create.drop', params: { actor, name: orWord(name, 'audit.aFile') } };
      }
      if (detail(d, 'kind') === 'dir') {
        return { key: 'audit.node.create.folder', params: { actor, name: orWord(name, 'audit.aFolder') } };
      }
      return { key: 'audit.node.create', params: { actor, name: orWord(name, 'audit.aFile') } };
    }
    case 'node.lock':
      return { key: d?.relock === true ? 'audit.node.lock.relock' : 'audit.node.lock', params: { actor } };
    case 'node.move':
      return {
        key: 'audit.node.move',
        params: { actor, name: orWord(nestedName(d, 'to') || nestedName(d, 'from'), 'audit.anItem') },
      };
    case 'node.rename': {
      const from = detail(d, 'from');
      const to = detail(d, 'to');
      if (from === '' || to === '') return { key: 'audit.node.rename.plain', params: { actor } };
      return { key: 'audit.node.rename', params: { actor, from: { text: from }, to: { text: to } } };
    }
    case 'node.shortcut': {
      const name = detail(d, 'name');
      if (name === '') return { key: 'audit.node.shortcut.plain', params: { actor } };
      return { key: 'audit.node.shortcut', params: { actor, name: { text: name } } };
    }
    case 'node.unlock': {
      const reason = detail(d, 'reason');
      if (reason === 'editor') return { key: 'audit.node.unlock.editor', params: { actor } };
      if (reason === 'relock') return { key: 'audit.node.unlock.relock', params: {} };
      return { key: 'audit.node.unlock', params: {} };
    }
    case 'node.versions_pruned':
      return { key: 'audit.node.versions_pruned', params: {} };
    case 'node.label_clear':
      return { key: 'audit.node.label_clear', params: { actor } };
    case 'node.label_set':
      return { key: 'audit.node.label_set', params: { actor } };
    case 'playlist.create':
      return { key: 'audit.playlist.create', params: { actor } };
    case 'playlist.rename':
      return { key: 'audit.playlist.rename', params: { actor } };
    case 'playlist.delete':
      return { key: 'audit.playlist.delete', params: { actor } };
    case 'playlist.entries':
      return { key: 'audit.playlist.entries', params: { actor } };
    case 'node.link':
      return { key: 'audit.node.link', params: { actor } };
    case 'node.copy':
      return { key: 'audit.node.copy', params: { actor } };
    case 'node.office':
      return { key: 'audit.node.office', params: { actor } };
    case 'node.version.delete':
      return { key: 'audit.node.version.delete', params: { actor } };
    case 'node.version.restore':
      return { key: 'audit.node.version.restore', params: { actor } };
    case 'node.version.update':
      return { key: 'audit.node.version.update', params: { actor } };
    case 'node.write':
      return { key: 'audit.node.write', params: { actor } };
    case 'node.zip':
      return { key: 'audit.node.zip', params: { actor } };
    case 'upload.create':
      return { key: 'audit.upload.create', params: { actor, name: orWord(detail(d, 'name'), 'audit.aFile') } };
    case 'upload.delete':
      return { key: 'audit.upload.delete', params: { actor } };
    case 'upload.expire':
      return { key: 'audit.upload.expire', params: { actor, name: orWord(detail(d, 'name'), 'audit.aFile') } };
    case 'user.files_moved': {
      const to = detail(d, 'to');
      const recipient = to !== '' ? names.person(to) : undefined;
      return {
        key: 'audit.user.files_moved',
        params: { actor, target, to: recipient !== undefined ? { text: recipient } : { key: 'audit.aUser' } },
      };
    }
    case 'user.login':
      return { key: 'audit.user.login', params: { actor } };
    case 'user.login_failed':
      return { key: 'audit.user.login_failed', params: { actor } };
    case 'user.logout':
      return { key: 'audit.user.logout', params: { actor } };
    case 'user.sign_in_link':
      return { key: 'audit.user.sign_in_link', params: { actor, target } };
    // A confirmation mail that was not sent (over its cap, or refused by the mailer) says so (sec2, decision 86).
    case 'user.device_confirm_sent':
      return {
        key: entry.outcome === 'capped' || entry.outcome === 'failed'
          ? 'audit.user.device_confirm_not_sent'
          : 'audit.user.device_confirm_sent',
        params: { actor },
      };
    case 'user.device_confirmed':
      return { key: 'audit.user.device_confirmed', params: { actor } };
    case 'user.login_confirm_requested':
      return { key: 'audit.user.login_confirm_requested', params: { actor } };
    case 'user.tokens_revoke':
      return { key: 'audit.user.tokens_revoke', params: { actor, target } };
    case 'webhook.create':
      return { key: 'audit.webhook.create', params: { actor, url: orWord(detail(d, 'url'), 'audit.aWebAddress') } };
    case 'webhook.delete':
      return { key: 'audit.webhook.delete', params: { actor, url: orWord(detail(d, 'url'), 'audit.aWebAddress') } };
    case 'webhook.retry':
      return { key: 'audit.webhook.retry', params: { actor, url: orWord(detail(d, 'url'), 'audit.aWebAddress') } };
    case 'webhook.secret':
      return { key: 'audit.webhook.secret', params: { actor, url: orWord(detail(d, 'url'), 'audit.aWebAddress') } };
    case 'webhook.test':
      return { key: 'audit.webhook.test', params: { actor, url: orWord(detail(d, 'url'), 'audit.aWebAddress') } };
    case 'webhook.update':
      return { key: 'audit.webhook.update', params: { actor, url: orWord(detail(d, 'url'), 'audit.aWebAddress') } };
    case 'webhook.disabled':
      return {
        key: webhookDisabledKey(detail(d, 'reason')),
        params: { url: orWord(detail(d, 'url'), 'audit.aWebAddress') },
      };
    case 'webhook.rewrap':
      return { key: d?.restored === true ? 'audit.webhook.rewrap.restored' : 'audit.webhook.rewrap', params: {} };
    // Imports from an archive (decision 92). The row's counts say how it went; the sentence says that it ended.
    case 'import.start':
      return { key: 'audit.import.start', params: { actor } };
    case 'import.finish':
      return { key: 'audit.import.finish', params: { actor } };
    case 'import.pause':
      return { key: 'audit.import.pause', params: { actor } };
    case 'import.resume':
      return { key: 'audit.import.resume', params: { actor } };
    case 'import.cancel':
      return { key: 'audit.import.cancel', params: { actor } };
    case 'imports.archive':
      return { key: flag(d, 'enabled') === false ? 'audit.imports.archive.off' : 'audit.imports.archive.on', params: { actor } };
    case 'templates.folder':
      return { key: d.node ? 'audit.templates.folder.set' : 'audit.templates.folder.clear', params: { actor } };
    default:
      return { key: 'audit.unlisted', params: { actor } };
  }
}

// webhookDisabledKey says why the system disabled a webhook. The reasons come from the server's delivery queue.
function webhookDisabledKey(reason: string): string {
  switch (reason) {
    case 'failing':
      return 'audit.webhook.disabled.failing';
    case 'overflow':
      return 'audit.webhook.disabled.overflow';
    case 'secret_unreadable':
      return 'audit.webhook.disabled.secret';
    default:
      return 'audit.webhook.disabled';
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

// isolateText wraps text in directional isolate marks in every language, so a name in the other direction keeps its order
// inside a sentence of this direction. Empty text stays empty. (i18n's isolate does the same for right-to-left languages
// only, for sizes and numbers.)
export function isolateText(text: string): string {
  if (text === '') return '';
  return String.fromCharCode(0x2068) + text + String.fromCharCode(0x2069);
}

// The isolate marks U+2066 to U+2069, which a translator may put around each parameter.
const ISOLATE_MARKS = /[⁦-⁩]/g;

// renderSentence turns a sentence into text. translate looks a catalogue key up with parameters, so the same
// function serves any language; text parameters are isolated, and key parameters are translated first.
export function renderSentence(s: Sentence, translate: (key: string, params?: Record<string, string>) => string): string {
  const main = fill(s.key, s.params, translate);
  if (!s.via) return main;
  return `${main} ${fill(s.via.key, s.via.params, translate)}`;
}

// fill translates one sentence. A generic word the catalogue puts first is capitalised ("A person verified their
// account"); the same word in the middle of a sentence is not. The word that leads is found by translating the key with
// a marker for each parameter, so the catalogue decides and no word is listed here. A name is text, and is never changed.
function fill(key: string, refs: Record<string, Ref>, translate: (key: string, params?: Record<string, string>) => string): string {
  const markers: Record<string, string> = {};
  for (const name of Object.keys(refs)) markers[name] = `\u0001${name}\u0002`;
  // The probe is read without isolate marks: a right-to-left translation wraps each marker in them (decision 67).
  const probe = Object.keys(refs).length > 0 ? translate(key, markers).replace(ISOLATE_MARKS, '') : '';
  const lead = Object.keys(refs).find((name) => probe.startsWith(markers[name]) && 'key' in refs[name]);
  return translate(key, refParams(refs, translate, lead));
}

function refParams(
  refs: Record<string, Ref>,
  translate: (key: string, params?: Record<string, string>) => string,
  lead?: string,
): Record<string, string> {
  const params: Record<string, string> = {};
  for (const [name, ref] of Object.entries(refs)) {
    if ('text' in ref) {
      params[name] = isolateText(ref.text);
    } else {
      const word = translate(ref.key);
      params[name] = name === lead ? word.charAt(0).toLocaleUpperCase() + word.slice(1) : word;
    }
  }
  return params;
}
