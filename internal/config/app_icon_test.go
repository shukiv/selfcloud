// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"strings"
	"testing"
)

// TestAppIconFileExtension: ui.brand.app_icon_file is a .png file only, the picture of the installed app's icons. An SVG
// is refused here, as the check of its bytes refuses one.
func TestAppIconFileExtension(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		file, wantErr string
	}{
		{"/srv/app.PNG", ""},
		{"app.png", ""},
		{"app.svg", "must be a .png"},
		{"app.jpg", "must be a .png"},
		{"app", "must be a .png"},
	} {
		cfg := Defaults()
		cfg.DataDir = "data"
		cfg.UI.Brand.AppIconFile = tc.file
		err := cfg.Validate()
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("app_icon_file %q refused: %v", tc.file, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), "app_icon_file")):
			t.Errorf("app_icon_file %q: %v, want %q naming app_icon_file", tc.file, err, tc.wantErr)
		}
	}
}

// TestAppIconPictureRow: the app icon's row is a picture's row, as the logo's and the icon's are: not a setting, not
// nameable by the settings API, and left out of ApplyStored without a word.
func TestAppIconPictureRow(t *testing.T) {
	t.Parallel()
	c := loadYAML(t, "", nil)
	var skipped []string
	c.ApplyStored(map[string]string{AppIconPicture: "image/png sha256:" + strings.Repeat("c", 64)},
		func(k, _ string) { skipped = append(skipped, k) })
	if len(skipped) != 0 {
		t.Errorf("skipped %v, want nothing", skipped)
	}
	if _, ok := c.StoredText(AppIconPicture); ok {
		t.Error("the app icon's row is held as a setting")
	}
	for _, key := range []string{AppIconPicture, "ui.brand.app_icon_file"} {
		ce := changeError(t, c.CheckKeys([]string{key}))
		if len(ce.Unknown) != 1 || ce.Unknown[0] != key {
			t.Errorf("%s: unknown = %v", key, ce.Unknown)
		}
		if _, err := SettingText(key, []byte(`"x"`)); err == nil {
			t.Errorf("SettingText(%s) accepted a value", key)
		}
	}
	if !IsPicture(AppIconPicture) || IsPicture("ui.brand.app_icon_file") {
		t.Error("IsPicture names the wrong keys for the app icon")
	}
}

// TestAppIconPictureIsLockedByItsFileKey: the file or the environment writing ui.brand.app_icon_file locks the app icon,
// even when it writes it empty; nothing else does, and it locks no other picture.
func TestAppIconPictureIsLockedByItsFileKey(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		yaml string
		env  map[string]string
		want bool
	}{
		{"", nil, false},
		{"ui:\n  brand:\n    app_icon_file: /srv/brand/app.png\n", nil, true},
		{"ui:\n  brand:\n    app_icon_file: \"\"\n", nil, true},
		{"", map[string]string{"SELFCLOUD_UI_BRAND_APP_ICON_FILE": "/srv/brand/app.png"}, true},
		{"ui:\n  brand:\n    name: Acme\n    icon_file: /srv/brand/icon.png\n", nil, false},
	} {
		c := loadYAML(t, tc.yaml, tc.env)
		if got := c.PictureLocked(AppIconPicture); got != tc.want {
			t.Errorf("%q %v: app icon locked = %v", tc.yaml, tc.env, got)
		}
		if c.PictureLocked(LogoPicture) || c.PictureLocked(IconPicture) != (tc.yaml != "" && strings.Contains(tc.yaml, "icon_file")) {
			t.Errorf("%q %v: the app icon's file locks another picture", tc.yaml, tc.env)
		}
	}
	// A configuration built in code locks by the path it holds.
	c := Defaults()
	c.UI.Brand.AppIconFile = "/srv/brand/app.png"
	if !c.PictureLocked(AppIconPicture) || c.PictureLocked(LogoPicture) || c.PictureLocked(IconPicture) {
		t.Error("a path set in code does not lock the app icon alone")
	}
}
