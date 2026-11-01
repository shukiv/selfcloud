// SPDX-License-Identifier: AGPL-3.0-or-later

// The sort checks (decision 47): a folder longer than one page is sorted by the server, in the browser. The largest file
// has the name that comes last, so it is on the last page by name. Sorted by size, descending, it must be the first row
// of the list without any scrolling, and ascending the smallest must be first. The same holds in a folder of Hebrew names.
const q = (v) => JSON.stringify(v);

// firstRow is the name of the first row of the list, and the window's scroll position, as the page shows them.
const firstRow = (page) =>
  page.evaluate(`(() => {
    const row = document.querySelector('.ant-table-tbody tr.ant-table-row');
    return { text: row ? row.textContent : '', scrollY: window.scrollY };
  })()`);

// setView shows List through the view toggle, which names the view it switches to (data-view); the words change with the
// language, so it is found by its test id.
async function setView(page, view) {
  const want = view === 'List' ? 'list' : 'grid';
  await page.waitFor(`!!document.querySelector('[data-testid="view-toggle"]')?.dataset.view`, 10000);
  await page.evaluate(`(() => { const b = document.querySelector('[data-testid="view-toggle"]'); if (b.dataset.view === ${JSON.stringify(want)}) b.click(); })()`);
}

export async function sortChecks(h, { page, base, accountKey, teamA }) {
  const { check } = h;
  const files = 120;
  // Each folder gets 120 files that grow by one byte, and one large file whose name sorts after all of them.
  async function seed(folder, name, large, largeBytes) {
    await h.api(base, 'POST', `/api/v1/folders/${teamA}/directories`, accountKey, { path: `/${folder}`, parents: false });
    for (let i = 1; i <= files; i++) {
      const path = `/${folder}/${name(i)}`;
      await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(path)}`, accountKey, Buffer.alloc(i, 'x'), 'text/plain');
    }
    await h.rawPut(base, `/api/v1/folders/${teamA}/files?path=${encodeURIComponent(`/${folder}/${large}`)}`, accountKey,
      Buffer.alloc(largeBytes, 'y'), 'application/octet-stream');
  }
  await seed('sort-long', (i) => `item-${String(i).padStart(3, '0')}.txt`, 'zz-largest.bin', 5000);
  await seed('סדר-ארוך', (i) => `קובץ-${String(i).padStart(3, '0')}.txt`, 'ת-גדול.bin', 5000);

  // The page is loaded again so the listing reads the folders that were just made, at desktop width.
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  await page.goto(`${page.base}/files/${teamA}`);
  await setView(page, 'List');
  await h.rowAppears(page, 'sort-long');
  await h.openRow(page, 'sort-long');
  await h.rowAppears(page, 'item-001.txt');

  // Size, ascending: the first click on the column sorts ascending, so the one-byte file is first.
  await h.clickButton(page, 'Size');
  await page.waitFor(`(() => { const r = document.querySelector('.ant-table-tbody tr.ant-table-row'); return !!r && r.textContent.includes('item-001.txt'); })()`, 15000);
  let top = await firstRow(page);
  check(top.text.includes('item-001.txt') && top.scrollY === 0, 'sorted by size ascending, the smallest file is the first row without scrolling');

  // Size, descending: the second click flips it, and the largest file, on the last page by name, is first.
  await h.clickButton(page, 'Size');
  await page.waitFor(`(() => { const r = document.querySelector('.ant-table-tbody tr.ant-table-row'); return !!r && r.textContent.includes('zz-largest.bin'); })()`, 15000);
  top = await firstRow(page);
  check(top.text.includes('zz-largest.bin') && top.scrollY === 0, 'sorted by size descending, the largest file is the first row without scrolling');

  // The same folder in Hebrew names: the sort is still by size, and the sort the person chose is kept for the next folder.
  await page.goto(`${page.base}/files/${teamA}`);
  await setView(page, 'List');
  await h.rowAppears(page, 'סדר-ארוך');
  await h.openRow(page, 'סדר-ארוך');
  await page.waitFor(`(() => { const r = document.querySelector('.ant-table-tbody tr.ant-table-row'); return !!r && r.textContent.includes(${q('ת-גדול.bin')}); })()`, 15000);
  top = await firstRow(page);
  check(top.text.includes('ת-גדול.bin') && top.scrollY === 0, 'in a Hebrew-named folder, sorted by size descending, the largest file is the first row');
}
