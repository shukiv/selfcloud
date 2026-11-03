// SPDX-License-Identifier: AGPL-3.0-or-later

// antdVars names each string token of antd's computed theme as a CSS variable, --ant-<kebab-case name>
// (colorTextSecondary is --ant-color-text-secondary), the names antd's own CSS variables would have. antd's CSS
// variables are off in this app (the ConfigProvider sets no cssVar), so these are what the app's stylesheet and inline
// styles read. ThemeProvider writes them on the root element whenever the theme changes.
export function antdVars(token: Record<string, unknown>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [key, value] of Object.entries(token)) {
    if (typeof value !== "string" || !/^[a-zA-Z0-9]+$/.test(key)) continue;
    out[`--ant-${key.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`)}`] = value;
  }
  return out;
}
