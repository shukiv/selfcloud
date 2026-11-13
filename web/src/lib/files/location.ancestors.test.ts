// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from 'vitest';
import { session, type Session } from '../session';
import { ancestorsOrNull, childrenPage } from './api';
import { locate } from './location';
import type { FolderAccess } from './types';

// The network is a stand-in for the base API function. It answers the reads a folder open makes, and it counts every
// request, in the order they start. A response waits for the gate, so a test can see what is in flight before any answer.
const net = vi.hoisted(() => ({
  nodes: new Map<string, Record<string, unknown>>(),
  hidden: new Set<string>(),
  ancestors: new Map<string, Record<string, unknown>[]>(),
  // truncated holds the directories whose answer the server marks truncated. Any other answer has no such field, as before.
  truncated: new Set<string>(),
  ancestorsStatus: 0,
  calls: [] as string[],
  gate: Promise.resolve(),
}));

vi.mock('../api', async (orig) => {
  const real = await orig<typeof import('../api')>();
  return {
    ...real,
    api: vi.fn(async (method: string, path: string) => {
      net.calls.push(`${method} ${path.split('?')[0]}`);
      await net.gate;
      const anc = /^\/api\/v1\/nodes\/([^/]+)\/ancestors$/.exec(path.split('?')[0]);
      if (anc) {
        if (net.ancestorsStatus) {
          throw new real.ApiError(net.ancestorsStatus, net.ancestorsStatus === 404 ? 'not_found' : 'internal', {}, 0);
        }
        if (net.hidden.has(anc[1])) throw new real.ApiError(404, 'not_found', {}, 0);
        return { ancestors: net.ancestors.get(anc[1]) ?? [], ...(net.truncated.has(anc[1]) ? { truncated: true } : {}) };
      }
      const stat = /^\/api\/v1\/nodes\/([^/]+)$/.exec(path.split('?')[0]);
      if (stat) {
        if (net.hidden.has(stat[1]) || !net.nodes.has(stat[1])) throw new real.ApiError(404, 'not_found', {}, 0);
        return net.nodes.get(stat[1]);
      }
      if (/\/children$/.test(path.split('?')[0])) return { items: [] };
      throw new Error(`unexpected request ${method} ${path}`);
    }),
  };
});

// The tree of the tests: a folder fld_a with its root nod_root, /a, /a/b, /a/b/c and a file /a/b/c/f.txt.
function dir(id: string, parent: string | null, name: string): Record<string, unknown> {
  return { id, folder_id: 'fld_a', parent_id: parent, name, kind: 'dir', role: 'manager', size: 0, type: '' };
}

const folder = { id: 'fld_a', name: 'Team A', role: 'editor', no_download_for_viewers: false } as unknown as FolderAccess;

const smallTree = () => [dir('nod_root', null, ''), dir('nod_a', 'nod_root', 'a'), dir('nod_b', 'nod_a', 'b'), dir('nod_c', 'nod_b', 'c')];

// deepTree is the folder's root and d1 to dN, each in the one before: dN is N levels below the root, with N ancestors.
function deepTree(depth: number): Record<string, unknown>[] {
  const out = [dir('nod_root', null, '')];
  for (let i = 1; i <= depth; i++) out.push(dir(`nod_d${i}`, i === 1 ? 'nod_root' : `nod_d${i - 1}`, `d${i}`));
  return out;
}

// setTree makes the stand-in network's tree for a caller who reaches the given nodes: each node's stat, and each directory's
// ancestors answer, topmost first, as the server gives it: it stops at the first node the caller does not reach, it holds at
// most the 64 nearest, and it is marked truncated when the caller reaches the folder above the 64th (decision 140).
function setTree(reached: string[], all = smallTree()): void {
  net.nodes.clear();
  net.hidden.clear();
  net.ancestors.clear();
  net.truncated.clear();
  for (const n of all) {
    net.nodes.set(n.id as string, n);
    if (!reached.includes(n.id as string)) net.hidden.add(n.id as string);
  }
  net.nodes.set('nod_file', { id: 'nod_file', folder_id: 'fld_a', parent_id: 'nod_c', name: 'f.txt', kind: 'file', role: 'manager', size: 0, type: 'text/plain' });
  const parentOf = new Map(all.map((n) => [n.id as string, n.parent_id as string | null]));
  for (const n of all) {
    const out: Record<string, unknown>[] = [];
    let id = parentOf.get(n.id as string) ?? null;
    for (; id !== null && out.length < 64; id = parentOf.get(id) ?? null) {
      if (!reached.includes(id)) break;
      out.unshift(net.nodes.get(id) as Record<string, unknown>);
    }
    net.ancestors.set(n.id as string, out);
    if (id !== null && out.length === 64 && reached.includes(id)) net.truncated.add(n.id as string);
  }
}

const ids = (nodes: { id: string }[]) => nodes.map((n) => n.id);
const dIDs = (from: number, to: number) => Array.from({ length: to - from + 1 }, (_, i) => `nod_d${from + i}`);

// open runs a folder open the way the files page does: its listing starts first, then the place is located.
async function open(dirID: string) {
  const listing = childrenPage('fld_a', dirID);
  const here = await locate('fld_a', dirID, [folder], listing);
  await listing;
  return here;
}

beforeEach(() => {
  session.set({ user: { id: 'usr_ann' }, account: null, csrf: 'c', publicLinks: false } as unknown as Session);
  net.calls.length = 0;
  net.gate = Promise.resolve();
  net.ancestorsStatus = 0;
  setTree(['nod_root', 'nod_a', 'nod_b', 'nod_c']);
});

const isStat = (c: string) => /^GET \/api\/v1\/nodes\/[^/]+$/.test(c);
const isAncestors = (c: string) => /\/ancestors$/.test(c);

describe('a folder open reads its trail in one request', () => {
  // The stat, the ancestors and the listing start together: no one of them waits for another.
  it('starts the directory stat, its ancestors and the listing before any of them answers', () => {
    let release: () => void = () => undefined;
    net.gate = new Promise<void>((r) => (release = r));
    const listing = childrenPage('fld_a', 'nod_c');
    const here = locate('fld_a', 'nod_c', [folder], listing);
    const started = [...net.calls];
    expect(started.filter(isStat)).toEqual(['GET /api/v1/nodes/nod_c']);
    expect(started.filter(isAncestors)).toEqual(['GET /api/v1/nodes/nod_c/ancestors']);
    expect(started.filter((c) => c.endsWith('/children'))).toHaveLength(1);
    release();
    return Promise.all([here, listing]);
  });

  it('sends three requests for a directory at depth 2, not one stat per level above it', async () => {
    await open('nod_b');
    expect(net.calls).toHaveLength(3);
    expect(net.calls.filter(isStat)).toEqual(['GET /api/v1/nodes/nod_b']);
    expect(net.calls.filter(isAncestors)).toHaveLength(1);
  });

  it('sends three requests for a directory at depth 3, not one stat per level above it', async () => {
    await open('nod_c');
    expect(net.calls).toHaveLength(3);
    expect(net.calls.filter(isStat)).toEqual(['GET /api/v1/nodes/nod_c']);
    expect(net.calls.filter(isAncestors)).toHaveLength(1);
  });
});

describe('the place a folder open shows does not change', () => {
  it('for a directory at depth 1: the root is reached, so the trail is empty and the path is /a', async () => {
    const here = await open('nod_a');
    expect(here).toMatchObject({
      folder: 'fld_a',
      folderName: 'Team A',
      folderRole: 'editor',
      rootReachable: true,
      trail: [],
      reach: [],
      path: '/a',
      dir: { id: 'nod_a', name: 'a' },
      dirRole: 'manager',
      spaceNoDownload: false,
    });
  });

  it('for a directory at depth 3: the trail is a and b, and the path is /a/b/c', async () => {
    const here = await open('nod_c');
    expect(here).toMatchObject({
      folderName: 'Team A',
      rootReachable: true,
      trail: [expect.objectContaining({ id: 'nod_a' }), expect.objectContaining({ id: 'nod_b' })],
      reach: [],
      path: '/a/b/c',
      dir: { id: 'nod_c', name: 'c' },
    });
  });

  it('for a directory below a grant: the root is not reached, so the place has no folder name, trail or path', async () => {
    // The person holds a grant on b: a and the root are not reached, and nothing above b is shown.
    setTree(['nod_b', 'nod_c']);
    const here = await open('nod_c');
    expect(here).toMatchObject({
      folder: 'fld_a',
      folderName: null,
      rootReachable: false,
      trail: [],
      path: null,
      reach: [expect.objectContaining({ id: 'nod_b' }), expect.objectContaining({ id: 'nod_c' })],
      dir: { id: 'nod_c', name: 'c' },
    });
  });

  it('is missing when the directory is not reached', async () => {
    setTree(['nod_root', 'nod_a']);
    expect(await open('nod_c')).toBeNull();
  });
});

describe('the place of a share link and of a folder root', () => {
  it('for a directory under a share link: the link node is the top the visitor sees, with no parent, so it is the root of the place', async () => {
    // The server hides the parent of a link node from an anyone-link visitor, so the link node reads as a root with no parent.
    // No anyone-link page calls locate(): the public browser reads its own path, and locate() is called only by the files
    // page and the gallery, for a signed-in person. So this case pins how the place is built for a node with no parent; no
    // page in this change reaches it with a share link.
    setTree(['nod_b', 'nod_c']);
    const linkNode = { ...(net.nodes.get('nod_b') as Record<string, unknown>), parent_id: null };
    net.nodes.set('nod_b', linkNode);
    net.ancestors.set('nod_c', [linkNode]);
    const here = await open('nod_c');
    expect(here).toMatchObject({
      folder: 'fld_a',
      folderName: 'Team A',
      rootReachable: true,
      trail: [],
      reach: [],
      path: '/c',
      dir: { id: 'nod_c', name: 'c' },
    });
  });

  it('for a folder root: the place is the root, with no trail, no ancestors read and the path /', async () => {
    const listing = childrenPage('fld_a', null);
    const here = await locate('fld_a', null, [folder], listing);
    await listing;
    expect(here).toMatchObject({
      folder: 'fld_a',
      folderName: 'Team A',
      folderRole: 'editor',
      dir: null,
      rootReachable: true,
      truncated: false,
      trail: [],
      reach: [],
      path: '/',
    });
    expect(net.calls.filter(isAncestors)).toHaveLength(0);
  });
});

describe('a folder deeper than 64 levels (decision 140)', () => {
  it('65 levels down: the 64 nearest folders are the trail, marked truncated, with no root, no space name and no path', async () => {
    const all = deepTree(65);
    setTree(ids(all as { id: string }[]), all);
    const here = await open('nod_d65');
    expect(here).toMatchObject({
      folder: 'fld_a',
      folderName: null,
      rootReachable: false,
      truncated: true,
      reach: [],
      path: null,
      dir: { id: 'nod_d65', name: 'd65' },
      dirRole: 'manager',
    });
    expect(ids(here!.trail)).toEqual(dIDs(1, 64));
  });

  it('64 levels down: the place is as it was, from the root, with the path from it and no mark', async () => {
    const all = deepTree(64);
    setTree(ids(all as { id: string }[]), all);
    const here = await open('nod_d64');
    expect(here).toMatchObject({
      folderName: 'Team A',
      rootReachable: true,
      truncated: false,
      reach: [],
      path: `/${dIDs(1, 64).map((id) => id.slice(4)).join('/')}`,
      dir: { id: 'nod_d64', name: 'd64' },
    });
    expect(ids(here!.trail)).toEqual(dIDs(1, 63));
  });

  it('70 levels down below a grant on the 64th: the server does not mark it, and the place is the grant place it was', async () => {
    const all = deepTree(70);
    setTree(dIDs(6, 70), all);
    const here = await open('nod_d70');
    expect(here).toMatchObject({ folderName: null, rootReachable: false, truncated: false, trail: [], path: null });
    expect(ids(here!.reach)).toEqual(dIDs(6, 70));
  });

  it('a directory a few levels down is not marked', async () => {
    expect(await open('nod_c')).toMatchObject({ truncated: false, rootReachable: true });
  });
});

describe('ancestorsOrNull', () => {
  it('answers the list and its mark, or null when the caller cannot reach the node', async () => {
    expect(await ancestorsOrNull('nod_c')).toEqual({ ancestors: net.ancestors.get('nod_c'), truncated: false });
    const all = deepTree(65);
    setTree(ids(all as { id: string }[]), all);
    expect((await ancestorsOrNull('nod_d65'))?.truncated).toBe(true);
    net.ancestorsStatus = 404;
    expect(await ancestorsOrNull('nod_c')).toBeNull();
  });

  it('throws any other error', async () => {
    net.ancestorsStatus = 500;
    await expect(ancestorsOrNull('nod_c')).rejects.toBeDefined();
  });
});
