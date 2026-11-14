// SPDX-License-Identifier: AGPL-3.0-or-later

// Unit tests for the section runner's bookkeeping (sections.mjs): a failed section is recorded and the run goes on,
// the summary names every failure in order, the exit code says the run failed, SELFCLOUD_E2E_STOP_FIRST stops at the
// first failure, and six failed sections stop the run. No browser: run.mjs supplies the recovery and the diagnostics.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSectionRunner, MAX_FAILED_SECTIONS } from './sections.mjs';

// harness collects what the runner asks of the run: the lines printed, the diagnostics kept and the recoveries, in order.
function harness(opts = {}) {
  const lines = [];
  const events = [];
  const runner = createSectionRunner({
    log: (line) => lines.push(line),
    saveDiagnostics: async (name) => {
      events.push(`save ${name}`);
    },
    recover: async (as) => {
      events.push(`recover ${as}`);
    },
    ...opts,
  });
  return { runner, lines, events };
}

const boom = (message) => async () => {
  throw new Error(message);
};

test('a failed section is recorded, and the next section still runs', async () => {
  const { runner, lines, events } = harness();
  let ran = false;
  await runner.section('d1', boom('check failed: the drawer'), { as: 'member' });
  await runner.section('d2', async () => {
    ran = true;
  }, { as: 'member' });
  assert.equal(ran, true);
  assert.deepEqual(runner.failures, [{ name: 'd1', error: 'check failed: the drawer' }]);
  assert.ok(lines.includes('SECTION FAILED d1: check failed: the drawer'));
  // The diagnostics are kept under the section's name before the page is recovered for the next section.
  assert.deepEqual(events, ['save d1', 'recover member']);
});

test('a passing section keeps no diagnostics and recovers nothing', async () => {
  const { runner, lines, events } = harness();
  await runner.section('d1', async () => {}, { as: 'member' });
  assert.deepEqual(runner.failures, []);
  assert.deepEqual(events, []);
  assert.deepEqual(lines, []);
  assert.equal(runner.exitCode, 0);
});

test('a section answers what its checks return; a failed one answers undefined', async () => {
  const { runner } = harness();
  const ids = await runner.section('myfiles', async () => ({ sharedID: 'n1' }), { as: 'member' });
  assert.deepEqual(ids, { sharedID: 'n1' });
  const none = await runner.section('myfiles', boom('no personal folder'), { as: 'member' });
  assert.equal(none, undefined);
});

test('the summary lists every failure in order, and a clean run prints none', async () => {
  const { runner, lines } = harness();
  await runner.section('a', boom('first'), { as: 'member' });
  await runner.section('b', async () => {}, { as: 'member' });
  await runner.section('c', boom('third'), { as: 'owner' });
  runner.printSummary();
  assert.deepEqual(lines.slice(-3), ['== e2e failures (2)', 'a: first', 'c: third']);
  const clean = harness();
  await clean.runner.section('a', async () => {}, { as: 'member' });
  clean.runner.printSummary();
  assert.deepEqual(clean.lines, []);
});

test('the exit code is 1 when any section failed, and 0 when none did', async () => {
  const { runner } = harness();
  assert.equal(runner.exitCode, 0);
  await runner.section('a', boom('first'), { as: 'member' });
  assert.equal(runner.exitCode, 1);
  await runner.section('b', async () => {}, { as: 'member' });
  assert.equal(runner.exitCode, 1);
});

test('STOP_FIRST stops at the first failed section, as the run did before', async () => {
  const { runner, events } = harness({ stopFirst: true });
  await assert.rejects(runner.section('a', boom('first'), { as: 'member' }), /first/);
  let ran = false;
  await runner.section('b', async () => {
    ran = true;
  }, { as: 'member' });
  assert.equal(ran, false);
  assert.equal(runner.failures.length, 1);
  assert.equal(runner.exitCode, 1);
  // The run's own end keeps the failure picture, as today: no per-section recovery on the stop-first path.
  assert.deepEqual(events, []);
});

test('six failed sections stop the run, and the rest are not run', async () => {
  assert.equal(MAX_FAILED_SECTIONS, 6);
  const { runner, lines, events } = harness();
  let ran = 0;
  for (let i = 1; i <= 8; i++) {
    await runner.section(`s${i}`, async () => {
      ran++;
      throw new Error(`fail ${i}`);
    }, { as: 'member' });
  }
  assert.equal(ran, 6);
  assert.equal(runner.failures.length, 6);
  assert.ok(lines.includes('stopping after 6 failed sections'));
  // The sixth failure stops the run: it is recorded and kept, but the page is not recovered again.
  assert.deepEqual(events, ['save s1', 'recover member', 'save s2', 'recover member', 'save s3', 'recover member', 'save s4', 'recover member', 'save s5', 'recover member', 'save s6']);
  assert.equal(runner.exitCode, 1);
  runner.printSummary();
  assert.equal(lines.at(-7), '== e2e failures (6)');
  assert.equal(lines.at(-1), 's6: fail 6');
});

test('FAKE_FAIL fails the named section before its checks run, and the rest run', async () => {
  const { runner } = harness({ fakeFail: 'd1' });
  let d1Ran = false;
  let d2Ran = false;
  await runner.section('d1', async () => {
    d1Ran = true;
  }, { as: 'member' });
  await runner.section('d2', async () => {
    d2Ran = true;
  }, { as: 'member' });
  assert.equal(d1Ran, false);
  assert.equal(d2Ran, true);
  assert.equal(runner.failures.length, 1);
  assert.equal(runner.failures[0].name, 'd1');
  assert.match(runner.failures[0].error, /SELFCLOUD_E2E_FAKE_FAIL=d1/);
  assert.equal(runner.exitCode, 1);
});

test('a recovery that fails ends the run', async () => {
  const { runner } = harness({
    recover: async () => {
      throw new Error('the browser is gone');
    },
  });
  await assert.rejects(runner.section('a', boom('first'), { as: 'member' }), /the browser is gone/);
  assert.equal(runner.failures.length, 1);
  assert.equal(runner.exitCode, 1);
});

test('a section that needs a failed producer\'s ids fails too, and is reported', async () => {
  const { runner } = harness();
  const mine = await runner.section('myfiles', boom('no personal folder'), { as: 'member' });
  await runner.section('owner-shared', async () => {
    const { sharedID } = mine;
    return sharedID;
  }, { as: 'owner' });
  assert.deepEqual(runner.failures.map((f) => f.name), ['myfiles', 'owner-shared']);
  assert.equal(runner.exitCode, 1);
});
