// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice p4 checks: labels, in the browser. p4Setup defines two fields and sets one value through the API as the
// account key. The owner's checks read Manage > Labels, define a field with the dialog, and set a text value in the
// details drawer; the member, who edits one team space and reads the other, sees the values as text where it only reads.
// When SHOTS_DIR is set, the checks also write screenshots there (light, dark, phone and Hebrew); the gate leaves it unset.
import { writeFile } from 'node:fs/promises';
import { join } from 'node:path';

const q = (v) => JSON.stringify(v);

// shot writes a PNG of the page when SHOTS_DIR is set, and does nothing otherwise.
async function shot(page, name) {
  const dir = process.env.SHOTS_DIR;
  if (!dir) return;
  // Let the drawer or the page finish its entrance; a frame taken during it shows a half-open panel.
  await new Promise((r) => setTimeout(r, 800));
  const { data } = await page.send('Page.captureScreenshot', { format: 'png' });
  await writeFile(join(dir, name), Buffer.from(data, 'base64'));
}

// clickText clicks the first element of the selector whose text is the given text.
async function clickText(page, selector, text) {
  await page.waitFor(`[...document.querySelectorAll(${q(selector)})].some((e) => e.textContent.trim() === ${q(text)})`);
  await page.evaluate(`(() => { [...document.querySelectorAll(${q(selector)})].find((e) => e.textContent.trim() === ${q(text)}).click(); return true; })()`);
}

// p4Setup makes the fields and a value as the account key: a choice field with two choices and a text field, and the
// choice value 'Open' on the report in team A.
export async function p4Setup(h, base, accountKey, { accountID, teamA }) {
  const status = await h.api(base, 'POST', `/api/v1/accounts/${accountID}/label-fields`, accountKey, {
    name: 'Status',
    type: 'choice',
    restricted: false,
    choices: [{ name: 'Open' }, { name: 'Done' }],
  });
  const open = status.choices.find((c) => c.name === 'Open');
  const items = (await h.api(base, 'GET', `/api/v1/folders/${teamA}/children?path=%2F&limit=500`, accountKey)).items;
  const report = items.find((n) => n.name === 'p3-report.txt');
  await h.api(base, 'PUT', `/api/v1/nodes/${report.id}/labels/${status.id}`, accountKey, { value: open.id });
  return { statusID: status.id, reportID: report.id };
}

// p4OwnerChecks, as the owner: the page lists the fields and defines one; the drawer shows the value and sets a text.
export async function p4OwnerChecks(page, h, { api, base, accountKey, teamA, reportID }) {
  await h.navigateTo(page, '/manage/labels');
  await page.waitFor(`!!document.querySelector('ul[aria-label="Labels"]') && document.querySelector('ul[aria-label="Labels"]').textContent.includes('Status')`, 15000);
  h.check(await page.evaluate(`document.querySelector('ul[aria-label="Labels"]').textContent.includes('Items with a value: 1')`), 'Manage > Labels lists the Status field, with one item holding a value');

  await h.clickButton(page, 'New field');
  await page.waitFor(`!!document.getElementById('label-form-name')`);
  await page.setInput('#label-form-name', 'Note');
  await h.clickButton(page, 'Create');
  await page.waitFor(`[...document.querySelectorAll('[role="status"]')].some((s) => s.textContent.trim() === 'Saved.')`, 15000);
  await page.waitFor(`document.querySelector('ul[aria-label="Labels"]').textContent.includes('Note')`, 15000);
  h.check(true, 'the owner defines a text field in the dialog, and the page lists it');
  await shot(page, 'labels-manage-light.png');

  await h.navigateTo(page, `/files/${teamA}`);
  await h.rowAppears(page, 'p3-report.txt');
  await h.selectRow(page, 'p3-report.txt');
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector('[data-testid="details"] section[aria-label="Labels"]')`, 15000);
  h.check(await page.evaluate(`document.querySelector('[data-testid="details"] section[aria-label="Labels"]').textContent.includes('Status')`), 'the details drawer lists the item\'s label fields');
  // The owner is a manager of the team space, and sets the new text field through its row.
  await page.setInput('[aria-label="Value for Note"]', 'checked by the owner');
  await page.evaluate(`(() => {
    const row = [...document.querySelectorAll('[data-testid="details"] section[aria-label="Labels"] > div')].find((d) => d.querySelector('[aria-label="Value for Note"]'));
    row.querySelectorAll('button')[0].click();
    return true;
  })()`);
  await page.waitFor(`!!document.querySelector('[data-testid="details"] section[aria-label="Labels"]')`);
  let saved = null;
  for (let i = 0; i < 40 && saved === null; i += 1) {
    const list = await api(base, 'GET', `/api/v1/nodes/${reportID}/labels`, accountKey);
    const note = list.items.find((it) => it.name === 'Note');
    if (note && note.value === 'checked by the owner') saved = note.value;
    else await new Promise((r) => setTimeout(r, 250));
  }
  h.check(saved === 'checked by the owner', 'the owner\'s text value is saved on the item, and read back by the API');
  await shot(page, 'labels-details-light.png');
  await h.press(page, '[data-testid="details"]', 'Escape');
  await page.waitFor(`!document.querySelector('[data-testid="details"]')`);

  // Dark, then phone without a horizontal scroll, then Hebrew right to left, on the labels page.
  await h.navigateTo(page, '/manage/labels');
  await page.waitFor(`document.querySelector('ul[aria-label="Labels"]')?.textContent.includes('Note')`, 15000);
  await clickText(page, '[data-testid="theme"] .ant-segmented-item-label', 'Dark');
  await page.waitFor(`document.documentElement.dataset.theme === 'dark'`);
  await shot(page, 'labels-manage-dark.png');
  await clickText(page, '[data-testid="theme"] .ant-segmented-item-label', 'Light');
  await page.waitFor(`document.documentElement.dataset.theme === 'light'`);
  h.check(true, 'the labels page shows in the dark theme as well');
  await page.send('Emulation.setDeviceMetricsOverride', { width: 375, height: 812, deviceScaleFactor: 1, mobile: true });
  await page.waitFor(`document.documentElement.scrollWidth <= document.documentElement.clientWidth`, 8000);
  h.check(true, 'the labels page has no horizontal scroll at phone width');
  await shot(page, 'labels-manage-phone.png');
  await page.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false });
  await h.clickOn(page, '[data-testid="language"]');
  await clickText(page, '.ant-dropdown-menu-item', 'עברית');
  // The list's own label is translated in Hebrew, so the list is found by its text, not by its name.
  await page.waitFor(`document.documentElement.dir === 'rtl' && [...document.querySelectorAll('ul')].some((u) => u.textContent.includes('Note'))`, 15000);
  await shot(page, 'labels-manage-hebrew.png');
  h.check(true, 'Hebrew lays the labels page out right to left');
  await h.clickOn(page, '[data-testid="language"]');
  await clickText(page, '.ant-dropdown-menu-item', 'English');
  await page.waitFor(`document.documentElement.dir === 'ltr'`);
}

// p4MemberChecks, as the member: the report's values as the member sees them, and a team space it only reads, where no
// label is set and the section is read-only.
export async function p4MemberChecks(page, h, { teamA, teamB }) {
  await h.navigateTo(page, `/files/${teamA}`);
  await h.rowAppears(page, 'p3-report.txt');
  await h.selectRow(page, 'p3-report.txt');
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector('[data-testid="details"] section[aria-label="Labels"]')`, 15000);
  h.check(
    await page.evaluate(`document.querySelector('[data-testid="details"] section[aria-label="Labels"]').textContent.includes('Open')`),
    'the member sees the owner\'s choice value on the report',
  );
  await h.press(page, '[data-testid="details"]', 'Escape');
  await page.waitFor(`!document.querySelector('[data-testid="details"]')`);

  await h.navigateTo(page, `/files/${teamB}`);
  await h.rowAppears(page, 'readme.txt');
  await h.selectRow(page, 'readme.txt');
  await h.clickButton(page, 'Details');
  await page.waitFor(`!!document.querySelector('[data-testid="details"] section[aria-label="Labels"]')`, 15000);
  h.check(
    await page.evaluate(`document.querySelector('[data-testid="details"] section[aria-label="Labels"]').textContent.includes('Not set')`),
    'a reader sees a label that is not set as Not set',
  );
  h.check(
    await page.evaluate(`!document.querySelector('[data-testid="details"] section[aria-label="Labels"] input') && !document.querySelector('[data-testid="details"] section[aria-label="Labels"] .ant-select')`),
    'a reader gets no label control',
  );
  await h.press(page, '[data-testid="details"]', 'Escape');
  await page.waitFor(`!document.querySelector('[data-testid="details"]')`);
}
