// SPDX-License-Identifier: AGPL-3.0-or-later

// The section runner's bookkeeping (decision 190), apart from the browser so node --test can exercise it
// (section.test.mjs). run.mjs supplies the recovery and the diagnostics; this module decides what a failed section
// does to the run: it is recorded, the run goes on to the next section, and after six failed sections (or the first
// one with SELFCLOUD_E2E_STOP_FIRST) the run stops. The summary at the end names every failed section in order.

export const MAX_FAILED_SECTIONS = 6;

// createSectionRunner makes the runner for one e2e run. recover(as) puts the page back for the next section and
// saveDiagnostics(name) keeps the failure picture under the section's name; both are run.mjs's, and a recovery that
// throws ends the run. log is console.log in the run, and a collector in the tests.
export function createSectionRunner({
  stopFirst = false,
  maxFailures = MAX_FAILED_SECTIONS,
  fakeFail = null,
  recover = async () => {},
  saveDiagnostics = async () => {},
  log = console.log,
} = {}) {
  const failures = [];
  let stopped = false;

  // section runs one section of the e2e run. A throw (a failed check, a wait's timeout, any error) ends the section,
  // not the run: the failure is recorded, the page's diagnostics are kept, and the page is recovered for the next
  // section. fn's answer goes back to the caller (a section's ids); a failed section answers undefined, so a section
  // that needs those ids fails too and is reported. Once the run has stopped, the remaining sections are not run.
  async function section(name, fn, opts = {}) {
    if (stopped) return undefined;
    try {
      // SELFCLOUD_E2E_FAKE_FAIL makes one section fail before its checks run: it is how the gate machinery is tested
      // without a broken build.
      if (fakeFail === name) throw new Error(`failed on purpose (SELFCLOUD_E2E_FAKE_FAIL=${name})`);
      return await fn();
    } catch (err) {
      failures.push({ name, error: err.message });
      log(`SECTION FAILED ${name}: ${err.message}`);
      if (stopFirst) {
        stopped = true;
        throw err;
      }
      await saveDiagnostics(name);
      if (failures.length >= maxFailures) {
        stopped = true;
        log(`stopping after ${maxFailures} failed sections`);
        return undefined;
      }
      await recover(opts.as ?? 'member');
      return undefined;
    }
  }

  // summaryLines is the end-of-run block: one line per failed section, in the order they failed. A run with no
  // failures has none.
  function summaryLines() {
    if (failures.length === 0) return [];
    return [`== e2e failures (${failures.length})`, ...failures.map((f) => `${f.name}: ${f.error}`)];
  }

  function printSummary() {
    for (const line of summaryLines()) log(line);
  }

  return {
    section,
    failures,
    summaryLines,
    printSummary,
    get stopped() {
      return stopped;
    },
    // The run's exit code: a failed section fails the run, even when every check after it passed.
    get exitCode() {
      return failures.length > 0 ? 1 : 0;
    },
  };
}
