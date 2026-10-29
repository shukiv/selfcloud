<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import { brand } from '../lib/brand';
  import { tr } from '../lib/i18n';

  // Placeholder stands in for a page another part of the web interface builds.
  let { titleKey }: { titleKey: string } = $props();
</script>

<h1 class="page-title">{$tr(titleKey)}</h1>
<p>{$tr('placeholder.comingSoon')}</p>
<p class="muted">{$tr('placeholder.body', { product: $brand.name })}</p>
