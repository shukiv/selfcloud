<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { tr } from '../lib/i18n';
  import { setTheme, theme, type Theme } from '../lib/theme';

  const choices: Theme[] = ['system', 'light', 'dark'];
</script>

<fieldset class="segmented">
  <legend class="sr-only">{$tr('theme.label')}</legend>
  {#each choices as choice (choice)}
    <label>
      <input
        type="radio"
        name="theme"
        value={choice}
        checked={$theme === choice}
        onchange={() => setTheme(choice)}
      />
      <span>{$tr(`theme.${choice}`)}</span>
    </label>
  {/each}
</fieldset>
