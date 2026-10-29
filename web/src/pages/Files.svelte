<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { get } from 'svelte/store';
  import { brand } from '../lib/brand';
  import { ApiError } from '../lib/apiError';
  import { go, location } from '../lib/router';
  import { lang, tr, translatePlural } from '../lib/i18n';
  import { session } from '../lib/session';
  import { folders, loadFolders } from '../lib/files/folders';
  import {
    childrenPage,
    copyNode,
    deleteNode,
    getJob,
    getMe,
    mkdir,
    moveNode,
    renameNode,
    restoreNode,
    serverLimits,
    type Transfer,
  } from '../lib/files/api';
  import { locate, joinPath, type Here } from '../lib/files/location';
  import { fileErrorText } from '../lib/files/fileErrors';
  import {
    nameProblem,
    nameMessageKey,
    uploadTargetFor,
    fromFileList,
    fromDataTransfer,
    startDownload,
    type DroppedFile,
  } from '../lib/files/ops';
  import { parseFilesPath, filesURL } from '../lib/files/route';
  import { trimCrumbs, type Crumb } from '../lib/files/breadcrumbs';
  import { sortNodes, nextSort, type SortKey } from '../lib/files/sort';
  import { clickSelect, moveFocus, selectAll, EMPTY, type Selection } from '../lib/files/selection';
  import { can, downloadAllowed, atLeast } from '../lib/files/roles';
  import { previewable } from '../lib/files/kind';
  import { readPrefs, writePrefs, type ViewPrefs } from '../lib/files/viewPrefs';
  import { UndoStack } from '../lib/files/undo';
  import type { ConflictChoice } from '../lib/files/conflict';
  import type { Job, Node } from '../lib/files/types';
  import { manager, uploads } from '../lib/files/uploads';
  import Breadcrumbs, { type Place } from '../components/files/Breadcrumbs.svelte';
  import Listing from '../components/files/Listing.svelte';
  import Details from '../components/files/Details.svelte';
  import Preview from '../components/files/Preview.svelte';
  import Versions from '../components/files/Versions.svelte';
  import PickDestination, { type Destination } from '../components/files/PickDestination.svelte';
  import Conflict from '../components/files/Conflict.svelte';
  import UndoToast from '../components/UndoToast.svelte';
  import Dialog from '../components/Dialog.svelte';
  import NotFound from './NotFound.svelte';

  // The file browser: a folder or a directory, its items, and what the signed-in person may do with them.
  // The server checks every operation again; the buttons only show what the role allows.

  type Status = 'loading' | 'ready' | 'missing' | 'error';
  type Dialogs = 'newFolder' | 'delete' | 'pick' | 'details' | 'versions' | 'preview';
  type Transferable = Transfer & { name: string };

  let personalID = $state<string | null>(null);
  let status = $state<Status>('loading');
  let errorMsg = $state('');
  let here = $state<Here | null>(null);
  let items = $state<Node[]>([]);
  let cursor = $state<string | undefined>(undefined);
  let loadingMore = $state(false);
  let prefs = $state<ViewPrefs>(readPrefs());
  // The selection keeps IDs, so a sort or a page that arrives later cannot move it onto other items.
  let sel = $state<Selection & { anchorID: string | null; focusID: string | null }>({
    ...EMPTY,
    anchorID: null,
    focusID: null,
  });
  let notice = $state<string | null>(null);
  let dragging = $state(false);
  let dragDepth = 0;
  let listEl = $state<HTMLElement | undefined>(undefined);
  let sentinel = $state<HTMLElement | undefined>(undefined);
  let fileInput = $state<HTMLInputElement | undefined>(undefined);
  let folderInput = $state<HTMLInputElement | undefined>(undefined);

  let renameID = $state<string | null>(null);
  let renameValue = $state('');
  let renameError = $state<string | null>(null);

  let dialog = $state<Dialogs | null>(null);
  let pickMode = $state<'move' | 'copy'>('move');
  let target = $state<Node | null>(null);
  let pending = $state<Node[]>([]);
  let previewItems = $state<Node[]>([]);
  let previewIndex = $state(0);
  let newName = $state('');
  let newNameError = $state<string | null>(null);
  let jobs = $state<Array<{ id: string; status: Job['status'] }>>([]);

  let conflictName = $state<string | null>(null);
  let conflictBatch = $state(false);
  let conflictResolve: ((a: { choice: ConflictChoice; remember: boolean }) => void) | null = null;

  const undoStack = new UndoStack();
  let undoView = $state<{ label: string; expiresAt: number; status: string } | null>(null);
  let undoTimer: ReturnType<typeof setTimeout> | null = null;

  let seenDone = 0;
  let loadToken = 0;

  let sorted = $derived(sortNodes(items, prefs.sort.key, prefs.sort.dir, $lang));
  let ids = $derived(sorted.map((n) => n.id));
  let selected = $derived(new Set(sel.ids));
  let byID = $derived(new Map(sorted.map((n) => [n.id, n])));
  let picked = $derived(sel.ids.map((id) => byID.get(id)).filter((n): n is Node => n !== undefined));
  let rovingID = $derived(sel.focusID && byID.has(sel.focusID) ? sel.focusID : (ids[0] ?? null));
  let folderRole = $derived(here ? (here.dir ? (here.dir.role ?? 'viewer') : here.folderRole) : 'viewer');
  let canUploadHere = $derived(here !== null && here.path !== null && atLeast(folderRole, 'contributor'));
  let canMakeFolder = $derived(canUploadHere);
  let allEdit = $derived(picked.length > 0 && picked.every((n) => can.edit(n.role)));
  let spaces = $derived.by(() => {
    const list = $folders;
    return personalID ? [...list.filter((f) => f.id === personalID), ...list.filter((f) => f.id !== personalID)] : list;
  });
  let view = $derived(prefs.view);

  let crumbs = $derived.by<Crumb<Place>[]>(() => {
    if (!here) return [];
    const rootLabel = here.folder === personalID ? $tr('nav.files') : (here.folderName ?? $tr('nav.files'));
    const out: Place[] = [];
    if (here.rootReachable) out.push({ key: `root-${here.folder}`, label: rootLabel, href: filesURL(here.folder, null) });
    for (const n of here.trail) out.push({ key: n.id, label: n.name, href: filesURL(here.folder, n.id) });
    if (here.dir) out.push({ key: here.dir.id, label: here.dir.name, href: filesURL(here.folder, here.dir.id) });
    return trimCrumbs(out, 4);
  });

  // ---- loading ----

  $effect(() => {
    // The address is the input: every change of it loads the place it names.
    void $location.path;
    void $location.search;
    void load();
  });

  onMount(() => {
    void serverLimits()
      .then((l) => manager.setChunkSize(l.max_chunk_size))
      .catch(() => undefined);
  });

  $effect(() => {
    // The folder upload picker takes directories; the attribute is set here because it is not in the typed
    // attribute list.
    folderInput?.setAttribute('webkitdirectory', '');
  });

  $effect(() => {
    // A finished upload adds to the listing, so the listing reloads once for each one.
    const done = $uploads.filter((u) => u.status === 'done').length;
    if (done > seenDone) void reload();
    seenDone = done;
  });

  $effect(() => {
    // Items past the first page load when the end comes into view; the button does the same.
    if (!sentinel || !cursor) return;
    const obs = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) void loadMore();
    });
    obs.observe(sentinel);
    return () => obs.disconnect();
  });

  async function load(): Promise<void> {
    const my = ++loadToken;
    const r = parseFilesPath($location.path);
    if (!r || r.kind === 'node') {
      status = 'missing';
      return;
    }
    status = 'loading';
    notice = null;
    try {
      if (r.kind === 'my') {
        const me = await getMe();
        if (my !== loadToken) return;
        if (!me.personal_folder_id) {
          status = 'missing';
          return;
        }
        personalID = me.personal_folder_id;
        go(filesURL(me.personal_folder_id, null), { replace: true });
        return;
      }
      if (!personalID) {
        personalID = (await getMe().catch(() => null))?.personal_folder_id ?? null;
      }
      const list = get(folders).length > 0 ? get(folders) : await loadFolders().catch(() => []);
      const dirID = r.kind === 'dir' ? r.dir : null;
      const where = await locate(r.folder, dirID, list);
      if (my !== loadToken) return;
      if (!where) {
        status = 'missing';
        return;
      }
      const page = await childrenPage(r.folder, dirID);
      if (my !== loadToken) return;
      here = where;
      items = page.items;
      cursor = page.next_cursor;
      sel = { ...EMPTY, anchorID: null, focusID: null };
      errorMsg = '';
      status = 'ready';
      await applyQuery(page.items);
    } catch (err) {
      if (my !== loadToken) return;
      if (err instanceof ApiError && err.code === 'not_found') {
        status = 'missing';
        return;
      }
      status = 'error';
      errorMsg = fileErrorText(err, $lang);
    }
  }

  // applyQuery honours ?select= (highlight an item) and ?preview= (open a file's preview), as a node link
  // does. A node past the first page is looked for on the pages after it.
  async function applyQuery(first: Node[]): Promise<void> {
    const q = new URLSearchParams($location.search);
    const want = q.get('preview') ?? q.get('select');
    if (!want) return;
    let found = first.find((n) => n.id === want);
    for (let guard = 0; !found && cursor && guard < 40; guard++) {
      const page = await childrenPage(here!.folder, here!.dir?.id ?? null, cursor);
      items = [...items, ...page.items];
      cursor = page.next_cursor;
      found = page.items.find((n) => n.id === want);
    }
    if (!found) return;
    sel = { ids: [found.id], anchor: null, focus: null, anchorID: found.id, focusID: found.id };
    if (q.get('preview') && previewable(found)) openPreview(found);
  }

  // reload reads the first page again, keeping the place, and drops selected items that are gone.
  async function reload(): Promise<void> {
    if (!here) return;
    try {
      const page = await childrenPage(here.folder, here.dir?.id ?? null);
      items = page.items;
      cursor = page.next_cursor;
      const present = new Set(page.items.map((n) => n.id));
      const keep = sel.ids.filter((id) => present.has(id));
      sel = { ids: keep, anchor: null, focus: null, anchorID: null, focusID: keep.length ? sel.focusID : null };
    } catch (err) {
      notice = fileErrorText(err, $lang);
    }
  }

  async function loadMore(): Promise<void> {
    if (!here || !cursor || loadingMore) return;
    loadingMore = true;
    try {
      const page = await childrenPage(here.folder, here.dir?.id ?? null, cursor);
      items = [...items, ...page.items];
      cursor = page.next_cursor;
    } catch (err) {
      notice = fileErrorText(err, $lang);
    } finally {
      loadingMore = false;
    }
  }

  // ---- selection and keys ----

  function setSel(next: Selection) {
    sel = {
      ids: next.ids,
      anchor: next.anchor,
      focus: next.focus,
      anchorID: next.anchor !== null ? (ids[next.anchor] ?? null) : null,
      focusID: next.focus !== null ? (ids[next.focus] ?? null) : null,
    };
  }

  // current is the selection with positions in the list as it is now.
  function current(): Selection {
    return {
      ids: sel.ids,
      anchor: sel.anchorID ? ids.indexOf(sel.anchorID) : null,
      focus: sel.focusID ? ids.indexOf(sel.focusID) : null,
    };
  }

  function onRowClick(index: number, e: MouseEvent) {
    setSel(clickSelect(current(), ids, index, { shift: e.shiftKey, toggle: e.ctrlKey || e.metaKey }));
  }

  function openNode(n: Node) {
    if (n.kind === 'dir') {
      if (here) go(filesURL(here.folder, n.id));
      return;
    }
    if (previewable(n)) {
      openPreview(n);
    } else {
      target = n;
      dialog = 'details';
    }
  }

  function openPreview(n: Node) {
    const list = sorted.filter((x) => previewable(x));
    previewItems = list;
    previewIndex = Math.max(0, list.findIndex((x) => x.id === n.id));
    dialog = 'preview';
  }

  function focusRow(id: string | null) {
    if (!id) return;
    void tick().then(() => listEl?.querySelector<HTMLElement>(`[data-id="${CSS.escape(id)}"]`)?.focus());
  }

  // columns is how many tiles sit on one row, read from the layout. A list has one column.
  function columns(): number {
    if (view === 'list' || !listEl) return 1;
    const tiles = Array.from(listEl.querySelectorAll<HTMLElement>('[data-id]'));
    if (tiles.length === 0) return 1;
    const top = tiles[0].offsetTop;
    const wrap = tiles.findIndex((t) => t.offsetTop !== top);
    return wrap === -1 ? tiles.length : wrap;
  }

  function onKey(e: KeyboardEvent) {
    if (renameID) return;
    const rtl = document.documentElement.dir === 'rtl';
    const cols = columns();
    let delta: number | null = null;
    switch (e.key) {
      case 'ArrowDown':
        delta = cols;
        break;
      case 'ArrowUp':
        delta = -cols;
        break;
      case 'ArrowRight':
        delta = view === 'grid' ? (rtl ? -1 : 1) : null;
        break;
      case 'ArrowLeft':
        delta = view === 'grid' ? (rtl ? 1 : -1) : null;
        break;
      case 'Home':
        delta = -ids.length;
        break;
      case 'End':
        delta = ids.length;
        break;
      default:
        break;
    }
    if (delta !== null) {
      e.preventDefault();
      const next = moveFocus(current(), ids, delta, e.shiftKey);
      setSel(next);
      focusRow(ids[next.focus ?? 0] ?? null);
      return;
    }
    if (e.key === 'Enter') {
      const n = sel.focusID ? byID.get(sel.focusID) : undefined;
      if (n) {
        e.preventDefault();
        openNode(n);
      }
    } else if (e.key === ' ') {
      e.preventDefault();
      const idx = sel.focusID ? ids.indexOf(sel.focusID) : -1;
      if (idx >= 0) setSel(clickSelect(current(), ids, idx, { toggle: true }));
    } else if (e.key === 'F2') {
      const n = picked.length === 1 ? picked[0] : undefined;
      if (n && can.edit(n.role)) {
        e.preventDefault();
        startRename(n);
      }
    } else if (e.key === 'Delete') {
      if (picked.length > 0 && allEdit) {
        e.preventDefault();
        askDelete(picked);
      }
    } else if (e.key === 'Backspace' || (e.altKey && e.key === 'ArrowUp')) {
      e.preventDefault();
      goUp();
    } else if ((e.ctrlKey || e.metaKey) && (e.key === 'a' || e.key === 'A')) {
      e.preventDefault();
      setSel(selectAll(ids));
    } else if (e.key === 'Escape') {
      setSel(EMPTY);
    }
  }

  function goUp() {
    if (!here || !here.dir) return;
    const parent = here.trail.length > 0 ? here.trail[here.trail.length - 1].id : null;
    go(filesURL(here.folder, parent));
  }

  // ---- rename ----

  function startRename(n: Node) {
    renameID = n.id;
    renameValue = n.name;
    renameError = null;
  }

  async function finishRename(commit: boolean) {
    const id = renameID;
    if (!id) return;
    const node = byID.get(id);
    renameID = null;
    if (!commit || !node) return;
    const value = renameValue.trim();
    if (value === node.name) return;
    const problem = nameProblem(value);
    if (problem) {
      renameError = $tr(nameMessageKey(problem));
      renameID = id;
      return;
    }
    try {
      const after = await renameNode(node.id, value, node.etag);
      const before = node.name;
      undoStack.push($tr('files.undo.renamed'), async () => {
        let bad = 0;
        try {
          // The ETag from the rename is the one the undo must match: a change in between fails the undo.
          await renameNode(node.id, before, after.etag);
        } catch {
          bad = 1;
        }
        await reload();
        return bad;
      });
      showUndo($tr('files.undo.renamed'));
      await reload();
    } catch (err) {
      renameError = fileErrorText(err, $lang);
      renameID = id;
    }
  }

  // ---- delete ----

  function askDelete(nodes: Node[]) {
    pending = nodes;
    dialog = 'delete';
  }

  async function confirmDelete() {
    const nodes = pending;
    dialog = null;
    const done: Node[] = [];
    let failed = 0;
    for (const n of nodes) {
      try {
        await deleteNode(n.id);
        done.push(n);
      } catch {
        failed++;
      }
    }
    if (done.length > 0) {
      undoStack.push($tr('files.undo.deleted'), async () => {
        let bad = 0;
        for (const n of done) {
          try {
            await restoreNode(n.id);
          } catch {
            bad++;
          }
        }
        await reload();
        return bad;
      });
      showUndo($tr('files.undo.deleted'));
    }
    if (failed > 0) notice = $tr('files.err.someFailed', { count: failed });
    await reload();
  }

  // ---- move and copy ----

  function startPick(mode: 'move' | 'copy', nodes: Node[]) {
    pickMode = mode;
    pending = nodes;
    dialog = 'pick';
  }

  // askConflict asks what to do with a taken name. The answer comes back when the person chooses.
  function askConflict(name: string, batch: boolean): Promise<{ choice: ConflictChoice; remember: boolean }> {
    conflictName = name;
    conflictBatch = batch;
    return new Promise((resolve) => {
      conflictResolve = resolve;
    });
  }

  function answerConflict(choice: ConflictChoice, remember: boolean) {
    const resolve = conflictResolve;
    conflictName = null;
    conflictResolve = null;
    resolve?.({ choice, remember });
  }

  async function runTransfer(mode: 'move' | 'copy', nodes: Node[], dest: Destination) {
    dialog = null;
    const moved: Array<{ node: Node; from: Transferable }> = [];
    let batch: ConflictChoice | null = null;
    let failed = 0;
    let jobsStarted = 0;
    const base: Transfer = dest.dir ? { folder: dest.folder, parent: dest.dir } : { folder: dest.folder, path: '/' };
    for (const n of nodes) {
      let choice: ConflictChoice | null = batch;
      for (;;) {
        const onConflict = choice === 'replace' ? 'replace' : choice === 'rename' ? 'rename' : 'fail';
        try {
          if (choice === 'skip') break;
          if (mode === 'move') {
            const after = await moveNode(n.id, { ...base, on_conflict: onConflict }, n.etag);
            moved.push({ node: after, from: { folder: n.folder_id, parent: n.parent_id ?? undefined, name: n.name } });
          } else {
            const res = await copyNode(n.id, { ...base, on_conflict: onConflict });
            if ('status' in res) {
              jobsStarted++;
              watchJob(res.id);
            }
          }
          break;
        } catch (err) {
          if (err instanceof ApiError && err.code === 'conflict' && onConflict === 'fail') {
            const answer = await askConflict(n.name, nodes.length > 1);
            if (answer.remember) batch = answer.choice;
            choice = answer.choice;
            continue;
          }
          failed++;
          notice = fileErrorText(err, $lang);
          break;
        }
      }
    }
    if (moved.length > 0) {
      undoStack.push($tr('files.undo.moved'), async () => {
        let bad = 0;
        for (const m of [...moved].reverse()) {
          try {
            await moveNode(m.node.id, { folder: m.from.folder, parent: m.from.parent, name: m.from.name }, m.node.etag);
          } catch {
            bad++;
          }
        }
        await reload();
        return bad;
      });
      showUndo($tr('files.undo.moved'));
    }
    if (failed > 0) notice = $tr('files.err.someFailed', { count: failed });
    else if (mode === 'copy' && jobsStarted === 0) notice = $tr('files.copy.done');
    await reload();
  }

  // watchJob polls a copy that the server runs as a job, until it finishes.
  function watchJob(id: string) {
    const idx = jobs.length;
    jobs = [...jobs, { id, status: 'queued' }];
    const poll = async () => {
      try {
        const j = await getJob(id);
        jobs = jobs.map((x, i) => (i === idx ? { id, status: j.status } : x));
        if (j.status === 'queued' || j.status === 'running') setTimeout(() => void poll(), 1500);
        else if (j.status === 'succeeded') await reload();
      } catch {
        jobs = jobs.map((x, i) => (i === idx ? { id, status: 'failed' } : x));
      }
    };
    void poll();
  }

  // ---- undo ----

  function showUndo(label: string) {
    const top = undoStack.current();
    if (!top) return;
    undoView = { label, expiresAt: top.expiresAt, status: '' };
    if (undoTimer) clearTimeout(undoTimer);
    undoTimer = setTimeout(dismissUndo, Math.max(0, top.expiresAt - Date.now()) + 50);
  }

  function dismissUndo() {
    if (undoTimer) clearTimeout(undoTimer);
    undoTimer = null;
    undoView = null;
    undoStack.clear();
  }

  async function doUndo() {
    const entry = undoStack.take();
    if (!entry) {
      dismissUndo();
      return;
    }
    const bad = await entry.run();
    undoView = {
      label: entry.label,
      expiresAt: Date.now() + 4000,
      status: bad > 0 ? translatePlural($lang, 'files.undo.partial', bad) : $tr('files.undo.done'),
    };
    if (undoTimer) clearTimeout(undoTimer);
    undoTimer = setTimeout(dismissUndo, 4000);
  }

  // ---- create folder ----

  function openNewFolder() {
    newName = '';
    newNameError = null;
    dialog = 'newFolder';
  }

  async function createFolder(e: SubmitEvent) {
    e.preventDefault();
    if (!here || here.path === null) return;
    const name = newName.trim();
    const problem = nameProblem(name);
    if (problem) {
      newNameError = $tr(nameMessageKey(problem));
      return;
    }
    const path = joinPath(here.path, name);
    if (!path) return;
    try {
      await mkdir(here.folder, path);
      dialog = null;
      notice = $tr('files.newFolder.created', { name });
      await reload();
    } catch (err) {
      newNameError = fileErrorText(err, $lang);
    }
  }

  // ---- uploads ----

  function queueFiles(files: DroppedFile[]) {
    if (!here) return;
    const batch: Array<{ file: File; target: NonNullable<ReturnType<typeof uploadTargetFor>> }> = [];
    let skipped = 0;
    for (const d of files) {
      const t = uploadTargetFor(here, d.relDir);
      if (t) batch.push({ file: d.file, target: t });
      else skipped++;
    }
    if (batch.length > 0) manager.add(batch);
    if (skipped > 0) notice = $tr('files.upload.noPath');
  }

  function onFilesPicked(e: Event) {
    const input = e.currentTarget as HTMLInputElement;
    if (input.files) queueFiles(fromFileList(input.files));
    input.value = '';
  }

  async function onDrop(e: DragEvent) {
    e.preventDefault();
    dragging = false;
    dragDepth = 0;
    if (!e.dataTransfer || !canUploadHere) return;
    queueFiles(await fromDataTransfer(e.dataTransfer));
  }

  function onDragOver(e: DragEvent) {
    if (!canUploadHere || !e.dataTransfer?.types.includes('Files')) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'copy';
  }

  function onDragEnter(e: DragEvent) {
    if (!canUploadHere || !e.dataTransfer?.types.includes('Files')) return;
    dragDepth++;
    dragging = true;
  }

  function onDragLeave() {
    dragDepth = Math.max(0, dragDepth - 1);
    if (dragDepth === 0) dragging = false;
  }

  // ---- other actions ----

  async function download(n: Node) {
    if (!downloadAllowed(n)) return;
    const ok = await startDownload(n.id).catch(() => false);
    if (!ok) notice = $tr('files.err.downloadRestricted');
  }

  function setPrefs(next: ViewPrefs) {
    prefs = next;
    writePrefs(next);
  }

  function sortBy(key: SortKey) {
    setPrefs({ ...prefs, sort: nextSort(prefs.sort, key) });
  }

  function reverseSort() {
    setPrefs({ ...prefs, sort: { key: prefs.sort.key, dir: prefs.sort.dir === 'asc' ? 'desc' : 'asc' } });
  }
</script>

<svelte:head>
  <title>{here?.dir?.name ?? here?.folderName ?? $tr('nav.files')} · {$brand.name}</title>
</svelte:head>

{#if status === 'missing'}
  <NotFound />
{:else}
  <section class="browser" class:dragging ondragenter={onDragEnter} ondragleave={onDragLeave} ondragover={onDragOver} ondrop={onDrop} aria-labelledby="browser-title">
    <header class="head">
      {#if here}
        <Breadcrumbs crumbs={crumbs} />
      {/if}
      <h1 id="browser-title" class="sr-only">{here?.dir?.name ?? here?.folderName ?? $tr('nav.files')}</h1>
    </header>

    {#if status === 'loading'}
      <div class="skeleton" aria-busy="true" role="status">
        <span class="sr-only">{$tr('files.loading')}</span>
        {#each Array(6) as _, i (i)}<div class="skel-row"></div>{/each}
      </div>
    {:else if status === 'error'}
      <p class="alert error" role="alert">{errorMsg}</p>
    {:else if here}
      {#if notice}
        <p class="alert" role="status">{notice}</p>
      {/if}

      <div class="toolbar" role="toolbar" aria-label={$tr('files.toolbar')}>
        {#if canMakeFolder}
          <button type="button" class="btn secondary" onclick={openNewFolder}>{$tr('files.act.newFolder')}</button>
        {/if}
        {#if canUploadHere}
          <button type="button" class="btn primary" onclick={() => fileInput?.click()}>{$tr('files.act.upload')}</button>
          <button type="button" class="btn secondary" onclick={() => folderInput?.click()}>{$tr('files.act.uploadFolder')}</button>
          <input bind:this={fileInput} type="file" multiple class="hidden-input" tabindex="-1" aria-hidden="true" onchange={onFilesPicked} />
          <input bind:this={folderInput} type="file" multiple class="hidden-input" tabindex="-1" aria-hidden="true" onchange={onFilesPicked} />
        {/if}
        <span class="spacer"></span>
        <div class="views" role="group" aria-label={$tr('files.view.label')}>
          <button type="button" class="btn secondary" aria-pressed={view === 'list'} onclick={() => setPrefs({ ...prefs, view: 'list' })}>{$tr('files.view.list')}</button>
          <button type="button" class="btn secondary" aria-pressed={view === 'grid'} onclick={() => setPrefs({ ...prefs, view: 'grid' })}>{$tr('files.view.grid')}</button>
        </div>
        {#if view === 'grid'}
          <label class="sort-label">
            <span class="sr-only">{$tr('files.sort.label')}</span>
            <select value={prefs.sort.key} onchange={(e) => sortBy((e.currentTarget as HTMLSelectElement).value as SortKey)}>
              <option value="name">{$tr('files.sort.name')}</option>
              <option value="modified">{$tr('files.sort.modified')}</option>
              <option value="size">{$tr('files.sort.size')}</option>
            </select>
          </label>
          <button type="button" class="btn secondary" onclick={reverseSort}>{$tr('files.sort.toggle')}</button>
        {/if}
      </div>

      {#if sel.ids.length > 0}
        <div class="selbar" role="toolbar" aria-label={$tr('files.selection.label')}>
          <p class="count" aria-live="polite">{translatePlural($lang, 'files.selected', sel.ids.length)}</p>
          {#if picked.length === 1 && picked[0].kind === 'file' && downloadAllowed(picked[0])}
            <button type="button" class="btn secondary" onclick={() => download(picked[0])}>{$tr('files.act.download')}</button>
          {/if}
          {#if picked.length === 1 && previewable(picked[0])}
            <button type="button" class="btn secondary" onclick={() => openPreview(picked[0])}>{$tr('files.act.preview')}</button>
          {/if}
          {#if picked.length === 1}
            <button type="button" class="btn secondary" onclick={() => { target = picked[0]; dialog = 'details'; }}>{$tr('files.act.details')}</button>
          {/if}
          {#if picked.length === 1 && picked[0].kind === 'file' && atLeast(picked[0].role, 'viewer')}
            <button type="button" class="btn secondary" onclick={() => { target = picked[0]; dialog = 'versions'; }}>{$tr('files.act.versions')}</button>
          {/if}
          {#if picked.length === 1 && can.edit(picked[0].role)}
            <button type="button" class="btn secondary" onclick={() => startRename(picked[0])}>{$tr('files.act.rename')}</button>
          {/if}
          {#if allEdit}
            <button type="button" class="btn secondary" onclick={() => startPick('move', picked)}>{$tr('files.act.move')}</button>
          {/if}
          {#if picked.length > 0}
            <button type="button" class="btn secondary" onclick={() => startPick('copy', picked)}>{$tr('files.act.copy')}</button>
          {/if}
          {#if allEdit}
            <button type="button" class="btn secondary danger" onclick={() => askDelete(picked)}>{$tr('files.act.delete')}</button>
          {/if}
          <button type="button" class="btn secondary" onclick={() => setSel(EMPTY)}>{$tr('files.act.clear')}</button>
        </div>
      {/if}

      <div class="content">
        <div class="list-wrap">
          {#if sorted.length === 0}
            <p class="empty" role="status">{$tr('files.empty')}</p>
          {:else}
            <Listing
              items={sorted}
              {view}
              {selected}
              focusID={rovingID}
              sortKey={prefs.sort.key}
              sortDir={prefs.sort.dir}
              {renameID}
              bind:renameValue
              {renameError}
              label={here.dir?.name ?? here.folderName ?? $tr('nav.files')}
              onclick={onRowClick}
              ondblclick={(i) => { const n = sorted[i]; if (n) openNode(n); }}
              onsort={sortBy}
              onrename={finishRename}
              onkeydown={onKey}
              onfocusitem={(i) => {
                const id = ids[i];
                if (id) sel = { ...sel, focusID: id };
              }}
              ondragover={onDragOver}
              ondragleave={onDragLeave}
              ondrop={onDrop}
              bind:listEl
            />
            {#if cursor}
              <div class="more">
                <button type="button" class="btn secondary" onclick={loadMore} disabled={loadingMore}>
                  {loadingMore ? $tr('files.loadingMore') : $tr('files.loadMore')}
                </button>
                <p class="muted note">{$tr('files.sortNote')}</p>
              </div>
              <div bind:this={sentinel} class="sentinel" aria-hidden="true"></div>
            {/if}
          {/if}
        </div>
      </div>

      {#if jobs.length > 0}
        <ul class="jobs" aria-label={$tr('files.jobs')}>
          {#each jobs as j (j.id)}
            <li>{j.status === 'succeeded' ? $tr('files.job.done') : j.status === 'failed' ? $tr('files.job.failed') : $tr('files.job.running')}</li>
          {/each}
        </ul>
      {/if}

      {#if dragging && canUploadHere}
        <div class="drop" aria-hidden="true">{$tr('files.drop.here')}</div>
      {/if}
    {/if}
  </section>

  {#if dialog === 'newFolder'}
    <Dialog title={$tr('files.newFolder.title')} id="newfolder" onclose={() => (dialog = null)}>
      <form onsubmit={createFolder}>
        <div class="field">
          <label for="newfolder-name">{$tr('files.newFolder.label')}</label>
          <input id="newfolder-name" type="text" bind:value={newName} aria-invalid={newNameError ? 'true' : undefined} aria-describedby={newNameError ? 'newfolder-error' : undefined} data-autofocus />
          {#if newNameError}<p class="field-error" id="newfolder-error">{newNameError}</p>{/if}
        </div>
        <div class="foot">
          <button type="button" class="btn secondary" onclick={() => (dialog = null)}>{$tr('common.cancel')}</button>
          <button type="submit" class="btn primary">{$tr('files.newFolder.submit')}</button>
        </div>
      </form>
    </Dialog>
  {/if}

  {#if dialog === 'delete'}
    <Dialog title={$tr('files.delete.title')} id="delete" onclose={() => (dialog = null)}>
      <p>{translatePlural($lang, 'files.delete.body', pending.length)}</p>
      <div class="foot">
        <button type="button" class="btn secondary" onclick={() => (dialog = null)}>{$tr('common.cancel')}</button>
        <button type="button" class="btn primary danger-fill" data-autofocus onclick={confirmDelete}>{$tr('files.delete.submit')}</button>
      </div>
    </Dialog>
  {/if}

  {#if dialog === 'pick'}
    <PickDestination
      mode={pickMode}
      {spaces}
      onpick={(dest) => runTransfer(pickMode, pending, dest)}
      onclose={() => (dialog = null)}
    />
  {/if}

  {#if dialog === 'details' && target}
    <Details node={target} canVersions={atLeast(target.role, 'viewer')} onclose={() => (dialog = null)} onversions={() => (dialog = 'versions')} />
  {/if}

  {#if dialog === 'versions' && target}
    <Versions node={target} onclose={() => (dialog = null)} onchanged={() => void reload()} />
  {/if}

  {#if dialog === 'preview' && previewItems.length > 0}
    <Preview items={previewItems} index={previewIndex} onclose={() => (dialog = null)} onindex={(i) => (previewIndex = i)} />
  {/if}

  {#if conflictName}
    <Conflict name={conflictName} batch={conflictBatch} onanswer={answerConflict} oncancel={() => answerConflict('skip', false)} />
  {/if}

  {#if undoView}
    <UndoToast label={undoView.label} expiresAt={undoView.expiresAt} status={undoView.status} onundo={doUndo} onexpire={dismissUndo} />
  {/if}
{/if}

<style>
  .browser {
    position: relative;
    display: grid;
    gap: var(--space-4);
    min-width: 0;
    min-height: 60dvh;
  }

  .browser.dragging {
    outline: 2px dashed var(--color-primary);
    outline-offset: 4px;
    border-radius: var(--radius-md);
  }

  .head {
    min-width: 0;
  }

  .toolbar,
  .selbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }

  .spacer {
    flex: 1 1 auto;
  }

  .views {
    display: inline-flex;
    gap: var(--space-1);
  }

  .selbar {
    padding: var(--space-2) var(--space-3);
    background: var(--color-primary-soft);
    border-radius: var(--radius-md);
  }

  .count {
    margin: 0 var(--space-2) 0 0;
    font-weight: 600;
  }

  .sort-label select {
    min-height: 44px;
    padding-inline: var(--space-2);
    font: inherit;
    color: var(--color-foreground);
    background: var(--color-surface);
    border: 1px solid var(--color-input-border);
    border-radius: var(--radius-sm);
  }

  .danger {
    color: var(--color-danger);
  }

  .danger-fill {
    background: var(--color-danger);
    color: var(--color-on-danger);
    border-color: var(--color-danger);
  }

  .list-wrap {
    min-width: 0;
  }

  .empty {
    padding: var(--space-8) var(--space-4);
    text-align: center;
    color: var(--color-muted-foreground);
    border: 1px dashed var(--color-border);
    border-radius: var(--radius-md);
  }

  .more {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--space-2);
    padding-block: var(--space-4);
  }

  .note,
  .muted {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-muted-foreground);
  }

  .skeleton {
    display: grid;
    gap: var(--space-2);
  }

  .skel-row {
    height: 44px;
    border-radius: var(--radius-sm);
    background: linear-gradient(90deg, var(--color-muted), var(--color-surface), var(--color-muted));
    background-size: 200% 100%;
    animation: shimmer 1.2s linear infinite;
  }

  @keyframes shimmer {
    from {
      background-position: 100% 0;
    }
    to {
      background-position: -100% 0;
    }
  }

  @media (prefers-reduced-motion: reduce) {
    .skel-row {
      animation: none;
    }
  }

  .drop {
    position: fixed;
    inset: var(--space-4);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 20;
    font-size: var(--text-lg);
    font-weight: 600;
    color: var(--color-primary);
    background: var(--color-primary-soft);
    border: 2px dashed var(--color-primary);
    border-radius: var(--radius-md);
    pointer-events: none;
  }

  .jobs {
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: var(--text-sm);
  }

  .hidden-input {
    position: absolute;
    width: 1px;
    height: 1px;
    opacity: 0;
    overflow: hidden;
  }

  .foot {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--space-2);
    margin-block-start: var(--space-4);
  }

  .field {
    display: grid;
    gap: var(--space-2);
  }

  .field-error {
    margin: 0;
    font-size: var(--text-sm);
    color: var(--color-danger);
  }
</style>
