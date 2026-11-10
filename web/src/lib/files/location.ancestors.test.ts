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
        return { ancestors: net.ancestors.get(anc[1]) ?? [] };
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

// chainOf answers the ancestors of a node the way the server does for a caller who reaches the given nodes.
function setTree(reached: string[]): void {
  const all = [dir('nod_root', null, ''), dir('nod_a', 'nod_root', 'a'), dir('nod_b', 'nod_a', 'b'), dir('nod_c', 'nod_b', 'c')];
  net.nodes.clear();
  net.hidden.clear();
  net.ancestors.clear();
  for (const n of all) {
    net.nodes.set(n.id as string, n);
    if (!reached.includes(n.id as string)) net.hidden.add(n.id as string);
  }
  net.nodes.set('nod_file', { id: 'nod_file', folder_id: 'fld_a', parent_id: 'nod_c', name: 'f.txt', kind: 'file', role: 'manager', size: 0, type: 'text/plain' });
  // Each directory's answer: its ancestors the caller reaches, topmost first, stopping at the first one it does not.
  const parentOf = new Map(all.map((n) => [n.id as string, n.parent_id as string | null]));
  for (const n of all) {
    const out: Record<string, unknown>[] = [];
    for (let id = parentOf.get(n.id as string) ?? null; id !== null; id = parentOf.get(id) ?? null) {
      if (!reached.includes(id)) break;
      out.unshift(net.nodes.get(id) as Record<string, unknown>);
    }
    net.ancestors.set(n.id as string, out);
  }
}

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
  // The stat, the ancestors and the listing start together: no one of them waits for another (perf-1 3.2).
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
    // A visitor of the link on b sees b with no parent (the server hides the link node's parent), and nothing above it.
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
      trail: [],
      reach: [],
      path: '/',
    });
    expect(net.calls.filter(isAncestors)).toHaveLength(0);
  });
});

describe('ancestorsOrNull', () => {
  it('answers the list, or null when the caller cannot reach the node', async () => {
    expect(await ancestorsOrNull('nod_c')).toEqual(net.ancestors.get('nod_c'));
    net.ancestorsStatus = 404;
    expect(await ancestorsOrNull('nod_c')).toBeNull();
  });

  it('throws any other error', async () => {
    net.ancestorsStatus = 500;
    await expect(ancestorsOrNull('nod_c')).rejects.toBeDefined();
  });
});
