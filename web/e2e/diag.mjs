// SPDX-License-Identifier: AGPL-3.0-or-later

// saveFailure keeps a picture of the page, its address, its errors and the last requests when a run fails. The folder is the
// one SELFCLOUD_E2E_DIAG names (scripts/ci-e2e.sh sets it to e2e-out/, which the CI job uploads on a failure). It never throws:
// a page that cannot answer in time gives up after a few seconds, and the run's own failure is what the run reports.
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const SECONDS = 10;

// within gives up on a promise that does not settle, so a page that cannot answer does not hold the run.
function within(promise, what) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${what} did not answer in ${SECONDS} s`)), SECONDS * 1000);
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

export async function saveFailure(page, name) {
  const dir = process.env.SELFCLOUD_E2E_DIAG;
  if (!dir || !page) return;
  try {
    await mkdir(dir, { recursive: true });
    const shot = await within(page.send('Page.captureScreenshot', { format: 'png' }), 'the screenshot');
    await writeFile(join(dir, `${name}-failure.png`), Buffer.from(shot.data, 'base64'));
    const where = await within(page.evaluate('location.href'), 'the page address').catch(() => 'unknown');
    const sent = (page.sent ?? []).slice(-10).map((r) => `${r.method} ${r.url.replace(/^https?:\/\/[^/]+/, '')}`);
    const lines = [
      `address: ${where}`,
      `console and page errors (${page.errors?.length ?? 0}):`,
      ...(page.errors ?? []),
      'last requests:',
      ...sent,
      '',
    ];
    await writeFile(join(dir, `${name}-failure.txt`), lines.join('\n'));
  } catch (err) {
    console.error(`could not keep the failure picture (${name}): ${err.message}`);
  }
}
