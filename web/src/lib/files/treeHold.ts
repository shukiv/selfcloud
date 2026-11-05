// SPDX-License-Identifier: AGPL-3.0-or-later

import { get, userWritable } from "../store";

// The sidebar's tree keeps some things outside the tree component, so a remount of the tree (the rail, the drawer, a breakpoint) does not
// undo them (decision 88): the folders on the way to the place that the person closed by hand, and the visit (serial) each failed read was
// first shown in. Both belong to the signed-in person (userWritable). The serial counts the changes of place: a hold lasts for one place,
// and a failed read's alert is shown while the place is the one it was first shown in.
interface Hold {
  place: string;
  closed: string[];
  serial: number;
  seen: Record<string, number>;
}

const hold = userWritable<Hold>({ place: "", closed: [], serial: 0, seen: {} });

// startPlace is told of each change of place (the empty text when the files pages are left). It returns true when the place changed: the
// tree then renders again, so each failed read is quiet at once. The new place holds nothing closed.
export function startPlace(place: string): boolean {
  const h = get(hold);
  if (h.place === place) return false;
  hold.set({ ...h, place, closed: [], serial: h.serial + 1 });
  return true;
}

// currentSerial is the number of changes of place so far.
export function currentSerial(): number {
  return get(hold).serial;
}

// closedFor lists the folders the person closed by hand on the way to this place.
export function closedFor(place: string): readonly string[] {
  const h = get(hold);
  return h.place === place ? h.closed : [];
}

// holdClose records a folder on the way to the place that the person closed by hand; holdOpen releases it.
export function holdClose(place: string, key: string): void {
  const h = get(hold);
  const closed = h.place === place ? h.closed : [];
  if (!closed.includes(key)) hold.set({ ...h, place, closed: [...closed, key] });
}

export function holdOpen(place: string, key: string): void {
  const h = get(hold);
  if (h.place === place && h.closed.includes(key)) hold.set({ ...h, closed: h.closed.filter((x) => x !== key) });
}

// failureSeenIn is the visit (serial) a folder's failed read was first shown in, or undefined when it has not been shown.
export function failureSeenIn(key: string): number | undefined {
  return get(hold).seen[key];
}

// noteFailure records the visit a failed read is first shown in; a read that failed before in this visit is not re-recorded.
export function noteFailure(key: string, serial: number): void {
  const h = get(hold);
  if (h.seen[key] !== undefined) return;
  hold.set({ ...h, seen: { ...h.seen, [key]: serial } });
}

// clearFailure makes a folder's next failure new again: its read succeeded, or the person asked to try again.
export function clearFailure(key: string): void {
  const h = get(hold);
  if (h.seen[key] === undefined) return;
  const seen = { ...h.seen };
  delete seen[key];
  hold.set({ ...h, seen });
}
