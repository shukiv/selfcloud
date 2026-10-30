// SPDX-License-Identifier: AGPL-3.0-or-later
// Recreates dist/.gitignore after a build. Vite empties dist/ first, so the file the repository keeps
// (and the Go embed needs) is written again here. Nothing else under dist/ is ever committed.
import { writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const target = fileURLToPath(new URL('../dist/.gitignore', import.meta.url));
await writeFile(target, '*\n!.gitignore\n');
console.log('dist/.gitignore written');
