// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Names } from '../audit/sentence';
import { collectAll } from './paging';
import { listPeople, listSpaces } from './api';
import { namesOf } from './people';

// NO_NAMES is the lookup before the names load, or when they cannot load: every name is unknown, so a sentence
// reads with its generic words and the list still shows.
export const NO_NAMES: Names = { person: () => undefined, space: () => undefined };

// loadNames reads the account's people and team spaces, so audit sentences can name them. The read is capped
// at 20 pages of each; a name beyond the cap reads with its generic word, which the audit page says in its
// note.
export async function loadNames(accountId: string): Promise<{ names: Names; complete: boolean }> {
  const [people, spaces] = await Promise.all([
    collectAll((cursor) => listPeople(accountId, cursor)),
    collectAll((cursor) => listSpaces(accountId, cursor)),
  ]);
  return {
    names: namesOf(people.items, spaces.items),
    complete: people.complete && spaces.complete,
  };
}
