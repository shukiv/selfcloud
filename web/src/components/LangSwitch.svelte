<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { LANGUAGES, NATIVE_NAMES, lang, setLang, tr, type Lang } from '../lib/i18n';
  import { PREF_LANG, writePref } from '../lib/prefs';
  import { session } from '../lib/session';
  import { setLocale } from '../lib/auth';

  // change applies the language at once and saves it: to the account when signed in (so every
  // device gets it), else to this browser.
  async function change(event: Event) {
    const next = (event.currentTarget as HTMLSelectElement).value as Lang;
    setLang(next);
    writePref(PREF_LANG, next);
    if ($session) {
      try {
        await setLocale(next);
      } catch {
        // Kept for this page; the account keeps its previous language.
      }
    }
  }
</script>

<label class="control">
  <span class="sr-only">{$tr('lang.label')}</span>
  <select value={$lang} onchange={change}>
    {#each LANGUAGES as code (code)}
      <option value={code} lang={code}>{NATIVE_NAMES[code]}</option>
    {/each}
  </select>
</label>
