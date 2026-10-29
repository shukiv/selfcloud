// SPDX-License-Identifier: AGPL-3.0-or-later

// Rules the installation admin pages use to read the URL, choose an expiry, move between tabs and tell whether an
// admin is the last one. The server decides every outcome; these only shape what the pages send and show.

export type TenantStatus = 'all' | 'active' | 'suspended';

// tenantQuery reads the tenants list's address: ?q= is the search text (at most 200 characters) and ?status= is
// one of all, active or suspended. Anything else reads as the default.
export function tenantQuery(search: string): { q: string; status: TenantStatus } {
  const p = new URLSearchParams(search);
  const q = (p.get('q') ?? '').trim().slice(0, 200);
  const s = p.get('status');
  const status: TenantStatus = s === 'active' || s === 'suspended' ? s : 'all';
  return { q, status };
}

// tenantSearch writes the list's address back from its state. The default values are left out.
export function tenantSearch(q: string, status: TenantStatus): string {
  const p = new URLSearchParams();
  if (q.trim() !== '') p.set('q', q.trim());
  if (status !== 'all') p.set('status', status);
  const s = p.toString();
  return s === '' ? '' : `?${s}`;
}

export type ExpiryChoice = 'never' | '30' | '90' | '365';

const DAY = 24 * 60 * 60 * 1000;

// expiryFor is the instant a new key ends, counted from now, or undefined for a key that never ends.
export function expiryFor(choice: ExpiryChoice, now: Date): string | undefined {
  if (choice === 'never') return undefined;
  return new Date(now.getTime() + Number(choice) * DAY).toISOString();
}

// tabIds are the tenant page's tabs, in order.
export const TENANT_TABS = ['overview', 'users', 'keys', 'audit'] as const;
export type TenantTab = (typeof TENANT_TABS)[number];

// tabFrom reads ?tab= for the tenant page, and the first tab when it names none or an unknown one.
export function tabFrom(search: string): TenantTab {
  const t = new URLSearchParams(search).get('tab');
  return (TENANT_TABS as readonly string[]).includes(t ?? '') ? (t as TenantTab) : 'overview';
}

// nextTab is the tab the arrow keys move to from current (Home and End go to the ends). Wrapping is kept, so the
// arrow keys never stop at an edge.
export function nextTab(current: TenantTab, key: string): TenantTab | null {
  const i = TENANT_TABS.indexOf(current);
  switch (key) {
    case 'ArrowRight':
    case 'ArrowDown':
      return TENANT_TABS[(i + 1) % TENANT_TABS.length];
    case 'ArrowLeft':
    case 'ArrowUp':
      return TENANT_TABS[(i - 1 + TENANT_TABS.length) % TENANT_TABS.length];
    case 'Home':
      return TENANT_TABS[0];
    case 'End':
      return TENANT_TABS[TENANT_TABS.length - 1];
    default:
      return null;
  }
}

// isLastActiveAdmin is true when removing the admin would leave no active admin. The page then disables the
// removal and says why. The server refuses it too, unless an admin key without an expiry can still act.
export function isLastActiveAdmin(admins: Array<{ id: string; status: string }>, target: string): boolean {
  const active = admins.filter((a) => a.status === 'active');
  return active.length === 1 && active[0].id === target;
}
