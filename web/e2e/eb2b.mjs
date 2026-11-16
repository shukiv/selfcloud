// SPDX-License-Identifier: AGPL-3.0-or-later

// Slice eb2b: Manage > Embedding, where the account's owner lists the sites that may frame the drive.
// As the owner: open the page through its sidebar entry, add a site, see it listed and read back by the
// API with the account key, remove it (the question names it first), and see the empty table. The
// account's embed list is left empty afterwards. Run alone with SELFCLOUD_E2E_ONLY=eb2b (run.mjs signs
// the owner in first); the full run reaches it among the owner's management pages.

const q = (v) => JSON.stringify(v);
const ORIGIN = 'https://host-eb2b.example';

export async function eb2bChecks(h, { page, base, accountID, accountKey }) {
  const { check, api, clickNav, clickButton } = h;
  const embed = `/api/v1/accounts/${accountID}/embed`;
  try {
    // Start from a known empty list, so the page opens on the empty table.
    await api(base, 'PUT', embed, accountKey, { origins: [] });

    await clickNav(page, 'Embedding');
    await page.waitFor(`!!document.querySelector('[role="region"][aria-label="Embedding"]')`, 20000);
    check(true, 'the owner opens Manage > Embedding from the sidebar');

    // Add the site through the dialog.
    await clickButton(page, 'Add site');
    await page.waitFor(`!!document.getElementById('embed-add-origin')`, 10000);
    await page.setInput('#embed-add-origin', ORIGIN);
    await clickButton(page, 'Add');
    await page.waitFor(
      `document.querySelector('[role="region"][aria-label="Embedding"]').textContent.includes(${q(ORIGIN)})`,
      15000,
    );
    check(true, 'the added site is listed on the page');

    // The account key reads the same list back.
    const listed = await api(base, 'GET', embed, accountKey);
    check(
      Array.isArray(listed.origins) && listed.origins.includes(ORIGIN),
      `GET .../embed with the account key lists the added origin (${JSON.stringify(listed.origins)})`,
    );

    // Remove it: the question names the origin, then the list is sent without it.
    await clickButton(page, `Remove: ${ORIGIN}`);
    await page.waitFor(`document.body.innerText.includes(${q(`Remove ${ORIGIN}?`)})`, 10000);
    await clickButton(page, 'Remove');
    await page.waitFor(
      `document.querySelector('[role="region"][aria-label="Embedding"]').textContent.includes('No sites yet')`,
      15000,
    );
    check(true, 'removing the site leaves the empty table');
    const after = await api(base, 'GET', embed, accountKey);
    check(
      Array.isArray(after.origins) && after.origins.length === 0,
      `the account's embed list is empty after the removal (${JSON.stringify(after.origins)})`,
    );
  } finally {
    await api(base, 'PUT', embed, accountKey, { origins: [] }).catch(() => undefined);
  }
}