<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->
<script lang="ts">
  import type { Kind } from '../../lib/files/kind';

  // Icon draws the kind of a node. It is decorative: the name beside it says what it is.
  let { kind, size = 24 }: { kind: Kind; size?: number } = $props();

  const PATHS: Record<Kind, string> = {
    folder: 'M3 6.5A1.5 1.5 0 0 1 4.5 5h4.2l2 2H19.5A1.5 1.5 0 0 1 21 8.5v9A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5z',
    file: 'M6 3h8l4 4v13a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zm7 1.5V8h3.5',
    image: 'M4 4h16v16H4zM4 16l4.5-4.5 3.5 3.5 2.5-2.5L20 17M9 8.5a1.5 1.5 0 1 0 0 .01',
    pdf: 'M6 3h8l4 4v13a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zm7 1.5V8h3.5M8 15h1.5a1 1 0 0 0 0-2H8v4',
    video: 'M4 6h11v12H4zM15 10l5-3v10l-5-3',
    audio: 'M9 17V6l10-2v11M9 17a2.5 2.5 0 1 1-2-2.45M19 15a2.5 2.5 0 1 1-2-2.45',
    text: 'M6 3h8l4 4v13a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zm2 9h8M8 16h6M8 12h8',
    office: 'M6 3h8l4 4v13a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zm7 1.5V8h3.5M8 13h8M8 16h5',
    archive: 'M4 4h16v4H4zM5 8h14v12H5zM10 12h4',
  };
</script>

<svg class="icon kind-{kind}" width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor"
  stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">
  <path d={PATHS[kind]} />
</svg>
