<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { lang, translate } from '../../lib/i18n';
  import { formatDate } from '../../lib/files/format';
  import { rank } from '../../lib/files/roles';
  import type { Permission, Role } from '../../lib/files/types';
  import { listPermissions } from '../../lib/sharing/api';
  import { auditLog, getPerson, listGroupMembers, listGroups, listSpaces } from '../../lib/manage/api';
  import { formatBytes } from '../../lib/files/format';
  import { setPersonLimit } from '../../lib/storage/api';
  import LimitEditor from '../storage/LimitEditor.svelte';
  import { collectAll } from '../../lib/manage/paging';
  import { personLabel, statusTone } from '../../lib/manage/people';
  import { failureText } from '../../lib/manage/failure';
  import type { Person } from '../../lib/manage/types';
  import type { AuditEntry, Names } from '../../lib/audit/sentence';
  import Dialog from '../Dialog.svelte';
  import Badge from '../Badge.svelte';
  import AuditItem from './AuditItem.svelte';

  // PersonDialog shows one person: the details, the team spaces and groups the person belongs to, and what the
  // person did. Each is read through the API. A person's My files are never listed, since they are private from
  // owners (docs/design.md, section 8).
  let {
    account,
    person,
    names,
    onclose,
  }: {
    account: string;
    person: Person;
    names: Names;
    onclose: () => void;
  } = $props();

  interface SpaceRow {
    id: string;
    name: string;
    role: Role;
  }

  let spaces = $state<SpaceRow[]>([]);
  let groups = $state<string[]>([]);
  let activity = $state<Array<AuditEntry & { id: string; time: string }>>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  // limit is the person's own storage limit as the server reads it: quota_bytes null follows the account default.
  let limit = $state<Pick<Person, 'quota_bytes' | 'effective_quota_bytes'>>(
    untrack(() => ({ quota_bytes: person.quota_bytes ?? null, effective_quota_bytes: person.effective_quota_bytes ?? null })),
  );
  let editingLimit = $state(false);
  let limitNotice = $state<string | null>(null);

  onMount(() => {
    void load();
  });

  // saveLimit sets the person's limit and reads the person again, so the dialog shows what the server applies.
  async function saveLimit(value: number | null) {
    await setPersonLimit(account, person.id, value);
    const p = await getPerson(account, person.id);
    limit = { quota_bytes: p.quota_bytes ?? null, effective_quota_bytes: p.effective_quota_bytes ?? null };
    editingLimit = false;
    limitNotice = translate($lang, 'storage.limitSaved');
  }

  function limitLine(): string {
    if (limit.effective_quota_bytes === null || limit.effective_quota_bytes === undefined) {
      return translate($lang, 'storage.noLimit');
    }
    return formatBytes(limit.effective_quota_bytes, $lang);
  }

  async function load() {
    loading = true;
    try {
      const [gs, fs, log, self] = await Promise.all([
        collectAll((c) => listGroups(account, c)),
        collectAll((c) => listSpaces(account, c)),
        auditLog(account, { actor: person.id, limit: 10 }),
        getPerson(account, person.id),
      ]);
      limit = { quota_bytes: self.quota_bytes ?? null, effective_quota_bytes: self.effective_quota_bytes ?? null };
      const inGroup = new Map<string, string>();
      for (const g of gs.items) {
        const members = await collectAll((c) => listGroupMembers(account, g.id, c));
        if (members.items.some((m) => m.user_id === person.id)) inGroup.set(g.id, g.name);
      }
      const rows: SpaceRow[] = [];
      for (const f of fs.items) {
        const perms: Permission[] = (await listPermissions(f.root_node_id)).items;
        const role = effectiveRole(person, perms, inGroup);
        if (role !== null) rows.push({ id: f.id, name: f.name, role });
      }
      spaces = rows;
      groups = [...inGroup.values()];
      activity = log.items;
      error = null;
    } catch (err) {
      error = failureText(err, $lang);
    } finally {
      loading = false;
    }
  }

  // effectiveRole is the highest role the person holds on a team space's root: an owner manages every space, and
  // otherwise a direct permission or one through a group the person is in counts. Null when it holds none.
  function effectiveRole(p: Person, perms: Permission[], inGroup: Map<string, string>): Role | null {
    if (p.role === 'owner') return 'manager';
    let best: Role | null = null;
    for (const perm of perms) {
      const mine =
        (perm.subject_kind === 'user' && perm.subject_id === p.id) ||
        (perm.subject_kind === 'group' && inGroup.has(perm.subject_id));
      if (mine && (best === null || rank(perm.role) > rank(best))) best = perm.role;
    }
    return best;
  }
</script>

<Dialog title={personLabel(person)} id="person" wide {onclose}>
  <div class="person">
    <dl class="details">
      <div><dt>{translate($lang, 'people.detail.email')}</dt><dd><bdi>{person.email}</bdi></dd></div>
      <div><dt>{translate($lang, 'people.detail.role')}</dt><dd>{translate($lang, `people.role.${person.role}`)}</dd></div>
      <div>
        <dt>{translate($lang, 'people.detail.status')}</dt>
        <dd><Badge tone={statusTone(person.status)}>{translate($lang, `people.status.${person.status}`)}</Badge></dd>
      </div>
      <div><dt>{translate($lang, 'people.detail.added')}</dt><dd>{formatDate(person.created_at, $lang)}</dd></div>
    </dl>

    {#if error}
      <p class="alert error" role="alert">{error}</p>
    {/if}
    <section id="person-limit" aria-labelledby="person-limit-title">
      <h3 id="person-limit-title">{translate($lang, 'storage.limit')}</h3>
      {#if limitNotice}<p class="alert success" role="status">{limitNotice}</p>{/if}
      {#if editingLimit}
        <LimitEditor
          current={limit.quota_bytes ?? null}
          allowDefault={true}
          onsave={saveLimit}
          oncancel={() => (editingLimit = false)}
        />
      {:else}
        <div class="limit-line">
          <p>
            {limitLine()}
            {#if limit.quota_bytes === null}<span class="muted"> · {translate($lang, 'storage.followsDefault')}</span>{/if}
          </p>
          <button type="button" class="btn secondary small" onclick={() => { limitNotice = null; editingLimit = true; }}>
            {translate($lang, 'storage.change')}
          </button>
        </div>
      {/if}
    </section>
    {#if loading}
      <p role="status">{translate($lang, 'app.loading')}</p>
    {:else}
      <section aria-labelledby="person-spaces">
        <h3 id="person-spaces">{translate($lang, 'people.detail.spaces')}</h3>
        {#if spaces.length === 0}
          <p class="muted">{translate($lang, 'people.detail.noSpaces')}</p>
        {:else}
          <ul class="plain">
            {#each spaces as s (s.id)}
              <li><bdi>{s.name}</bdi> — {translate($lang, `role.${s.role}`)}</li>
            {/each}
          </ul>
        {/if}
      </section>
      <section aria-labelledby="person-groups">
        <h3 id="person-groups">{translate($lang, 'people.detail.groups')}</h3>
        {#if groups.length === 0}
          <p class="muted">{translate($lang, 'people.detail.noGroups')}</p>
        {:else}
          <ul class="plain">
            {#each groups as g (g)}
              <li><bdi>{g}</bdi></li>
            {/each}
          </ul>
        {/if}
      </section>
      <section aria-labelledby="person-activity">
        <h3 id="person-activity">{translate($lang, 'people.detail.activity')}</h3>
        {#if activity.length === 0}
          <p class="muted">{translate($lang, 'people.detail.noActivity')}</p>
        {:else}
          <ul class="rows">
            {#each activity as entry (entry.id)}
              <AuditItem {entry} names={names} />
            {/each}
          </ul>
        {/if}
      </section>
    {/if}
  </div>
</Dialog>

<style>
  .person {
    display: grid;
    gap: var(--space-4);
  }

  .details {
    display: grid;
    gap: var(--space-2);
    margin: 0;
  }

  .details > div {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2);
    justify-content: space-between;
  }

  .details dt {
    color: var(--color-muted-foreground);
  }

  .details dd {
    margin: 0;
  }

  h3 {
    margin: 0 0 var(--space-2);
    font-size: var(--text-base);
    font-weight: 600;
  }

  .plain {
    margin: 0;
    padding-inline-start: var(--space-4);
  }

  .muted {
    margin: 0;
    color: var(--color-muted-foreground);
  }

  .limit-line {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-2);
  }

  .limit-line p {
    margin: 0;
  }
</style>
