// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"math"
	"strconv"
)

// minTextContrast is the WCAG ratio for body text.
const minTextContrast = 4.5

// The colours the interface uses when the operator sets none, as the web interface's design tokens
// have them. A pair is checked only when one of its colours is set by the operator.
const (
	defaultSidebarBg       = "#053D8E"
	defaultSidebarText     = "#FFFFFF"
	defaultSidebarActive   = "#FECC00"
	defaultSidebarOnActive = "#0D2142"
	defaultLightFill       = "#053D8E"
	defaultDarkFill        = "#2A5AA6"
)

// ContrastWarnings reports each explicit text-on-background pair under 4.5:1. The config is still
// accepted: an operator may choose a low-contrast brand on purpose, and the interface does not
// refuse it. The pairs are sidebar text on the sidebar, the active item's text on the active pill, and
// white on the accent fill of each theme.
func (b BrandConfig) ContrastWarnings() []string {
	type colour struct {
		key, value, fallback string
	}
	pick := func(c colour) (string, bool) {
		if c.value != "" {
			return c.value, true
		}
		return c.fallback, false
	}
	pairs := []struct {
		name   string
		fg, bg colour
	}{
		{"sidebar.text on sidebar.bg",
			colour{"sidebar.text", b.Sidebar.Text, defaultSidebarText},
			colour{"sidebar.bg", b.Sidebar.Bg, defaultSidebarBg}},
		{"sidebar.on_active on sidebar.active",
			colour{"sidebar.on_active", b.Sidebar.OnActive, defaultSidebarOnActive},
			colour{"sidebar.active", b.Sidebar.Active, defaultSidebarActive}},
		{"white on palette.light.accent_fill",
			colour{"white", "#FFFFFF", "#FFFFFF"},
			colour{"palette.light.accent_fill", b.Palette.Light.AccentFill, defaultLightFill}},
		{"white on palette.dark.accent_fill",
			colour{"white", "#FFFFFF", "#FFFFFF"},
			colour{"palette.dark.accent_fill", b.Palette.Dark.AccentFill, defaultDarkFill}},
	}
	var out []string
	for _, p := range pairs {
		fg, fgSet := pick(p.fg)
		bg, bgSet := pick(p.bg)
		if !fgSet && !bgSet {
			continue
		}
		ratio, ok := contrastRatio(fg, bg)
		if ok && ratio < minTextContrast {
			out = append(out, fmt.Sprintf("%s is %.2f:1, under %.1f:1", p.name, ratio, minTextContrast))
		}
	}
	return out
}

// contrastRatio is the WCAG ratio of two #rrggbb colours; ok is false when either is not one.
func contrastRatio(a, b string) (float64, bool) {
	la, ok1 := relativeLuminance(a)
	lb, ok2 := relativeLuminance(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05), true
}

// relativeLuminance is the WCAG relative luminance of a #rrggbb colour.
func relativeLuminance(hex string) (float64, bool) {
	if !accentPattern.MatchString(hex) {
		return 0, false
	}
	var lin [3]float64
	for i := range lin {
		v, err := strconv.ParseUint(hex[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return 0, false
		}
		c := float64(v) / 255
		if c <= 0.03928 {
			lin[i] = c / 12.92
		} else {
			lin[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*lin[0] + 0.7152*lin[1] + 0.0722*lin[2], true
}
