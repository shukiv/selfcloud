// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestMediaResumeRoundTrip: a stored place is read back, a later write replaces it, and a file with none reads as not found.
func TestMediaResumeRoundTrip(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	ctx := context.Background()
	if _, found, err := s.MediaResumeOf(ctx, "usr_b", "nod_n"); err != nil || found {
		t.Fatalf("no place stored: found = %v, err = %v; want not found", found, err)
	}
	at := time.Unix(80_000, 0)
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetMediaResume(ctx, "acc_n", "usr_b", "nod_n", MediaResume{PositionMS: 1500, DurationMS: 9000}, at)
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *Tx) error {
		return tx.SetMediaResume(ctx, "acc_n", "usr_b", "nod_n", MediaResume{PositionMS: 4200, DurationMS: 9000}, at.Add(time.Minute))
	}); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.MediaResumeOf(ctx, "usr_b", "nod_n")
	if err != nil || !found {
		t.Fatalf("read back: found = %v, err = %v", found, err)
	}
	if got.PositionMS != 4200 || got.DurationMS != 9000 || !got.UpdatedAt.Equal(at.Add(time.Minute)) {
		t.Fatalf("read back %+v, want the later write (4200 of 9000, a minute on)", got)
	}
}

// TestMediaResumeCascades: a place goes with its user, its file or its account, as the other per-user rows do.
func TestMediaResumeCascades(t *testing.T) {
	cases := map[string]string{
		"user":    "DELETE FROM users WHERE id = 'usr_b'",
		"node":    "DELETE FROM nodes WHERE id = 'nod_n'",
		"account": "DELETE FROM accounts WHERE id = 'acc_n'",
	}
	for name, del := range cases {
		t.Run(name, func(t *testing.T) {
			s := openTest(t)
			seedNotify(t, s)
			ctx := context.Background()
			if err := s.Write(ctx, func(tx *Tx) error {
				return tx.SetMediaResume(ctx, "acc_n", "usr_b", "nod_n", MediaResume{PositionMS: 1, DurationMS: 2}, time.Unix(1, 0))
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.w.ExecContext(ctx, del); err != nil {
				t.Fatal(err)
			}
			var n int
			if err := s.r.QueryRowContext(ctx, "SELECT COUNT(*) FROM media_resume").Scan(&n); err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Errorf("%s delete left %d media_resume rows; want 0", name, n)
			}
		})
	}
}

// playedFixture is seedNotify with three video and two audio files, and the places usr_b stopped at, newest first:
// nod_v1 6 s of 60 s at 5000, nod_a1 6 s of an unknown length at 4000, nod_v2 3 s of 60 s at 3000, nod_a2 55 s of 60 s
// at 2000 and nod_v3 30 s of 60 s at 1000. usr_a holds a place of his own in nod_n at 9000.
func playedFixture(t *testing.T) *Store {
	t.Helper()
	s := openTest(t)
	seedNotify(t, s)
	if err := exec(s, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at, type_group)
		VALUES
		('nod_v1', 'acc_n', 'fld_n', 'nod_n', 'v1.mp4', 'v1.mp4', 'file', 0, 'video/mp4', 0, 0, 'video'),
		('nod_v2', 'acc_n', 'fld_n', 'nod_n', 'v2.mp4', 'v2.mp4', 'file', 0, 'video/mp4', 0, 0, 'video'),
		('nod_v3', 'acc_n', 'fld_n', 'nod_n', 'v3.mp4', 'v3.mp4', 'file', 0, 'video/mp4', 0, 0, 'video'),
		('nod_a1', 'acc_n', 'fld_n', 'nod_n', 'a1.mp3', 'a1.mp3', 'file', 0, 'audio/mpeg', 0, 0, 'audio'),
		('nod_a2', 'acc_n', 'fld_n', 'nod_n', 'a2.mp3', 'a2.mp3', 'file', 0, 'audio/mpeg', 0, 0, 'audio')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, pl := range []struct {
		user, node   string
		pos, dur, at int64
	}{
		{"usr_b", "nod_v1", 6000, 60000, 5000},
		{"usr_b", "nod_a1", 6000, 0, 4000},
		{"usr_b", "nod_v2", 3000, 60000, 3000},
		{"usr_b", "nod_a2", 55000, 60000, 2000},
		{"usr_b", "nod_v3", 30000, 60000, 1000},
		{"usr_a", "nod_n", 1000, 2000, 9000},
	} {
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.SetMediaResume(ctx, "acc_n", pl.user, pl.node, MediaResume{PositionMS: pl.pos, DurationMS: pl.dur}, time.UnixMilli(pl.at))
		}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// playedIDs reads the node IDs of one played page.
func playedIDs(t *testing.T, s *Store, userID string, f ListFilter, unfinished bool, p Page) ([]string, []PlayedRow) {
	t.Helper()
	rows, err := s.PlayedPage(context.Background(), userID, f, unfinished, p)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, len(rows))
	for i, r := range rows {
		ids[i] = r.NodeID
	}
	return ids, rows
}

// TestPlayedPageNewestFirst: the places come back latest first, with the position, the duration and the time each was
// stored, and only the user's own.
func TestPlayedPageNewestFirst(t *testing.T) {
	s := playedFixture(t)
	ids, rows := playedIDs(t, s, "usr_b", ListFilter{}, false, Page{Limit: 10})
	want := []string{"nod_v1", "nod_a1", "nod_v2", "nod_a2", "nod_v3"}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Fatalf("played = %v, want %v", ids, want)
	}
	first := rows[0]
	if first.AccountID != "acc_n" || first.PositionMS != 6000 || first.DurationMS != 60000 || !first.UpdatedAt.Equal(time.UnixMilli(5000)) {
		t.Errorf("first row = %+v, want 6000 of 60000 in acc_n at 5000", first)
	}
}

// TestPlayedPagePaging: a limit of 2 over five places gives every place once, in order, and stops.
func TestPlayedPagePaging(t *testing.T) {
	s := playedFixture(t)
	ctx := context.Background()
	var got []string
	cursor := ""
	for i := 0; i < 10; i++ {
		rows, err := s.PlayedPage(ctx, "usr_b", ListFilter{}, false, Page{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			got = append(got, r.NodeID)
		}
		if len(rows) < 2 {
			break
		}
		cursor = PlayedCursorOf(rows[len(rows)-1], ListFilter{}, false)
	}
	want := []string{"nod_v1", "nod_a1", "nod_v2", "nod_a2", "nod_v3"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("paged = %v, want %v", got, want)
	}
}

// TestPlayedPageByType: a type keeps the places whose file has that type, newest first.
func TestPlayedPageByType(t *testing.T) {
	s := playedFixture(t)
	ids, _ := playedIDs(t, s, "usr_b", ListFilter{Group: "video"}, false, Page{Limit: 10})
	if strings.Join(ids, ",") != "nod_v1,nod_v2,nod_v3" {
		t.Errorf("video = %v", ids)
	}
	ids, _ = playedIDs(t, s, "usr_b", ListFilter{Group: "audio"}, false, Page{Limit: 10})
	if strings.Join(ids, ",") != "nod_a1,nod_a2" {
		t.Errorf("audio = %v", ids)
	}
}

// TestPlayedPageUnfinished: unfinished keeps a place worth resuming: 6 s of 60 s and 6 s of an unknown length stay,
// 3 s (under the start rule) and 55 s of 60 s (within the end rule) go, and a place mid-file stays.
func TestPlayedPageUnfinished(t *testing.T) {
	s := playedFixture(t)
	ids, _ := playedIDs(t, s, "usr_b", ListFilter{}, true, Page{Limit: 10})
	if strings.Join(ids, ",") != "nod_v1,nod_a1,nod_v3" {
		t.Errorf("unfinished = %v, want nod_v1,nod_a1,nod_v3", ids)
	}
	ids, _ = playedIDs(t, s, "usr_b", ListFilter{Group: "video"}, true, Page{Limit: 10})
	if strings.Join(ids, ",") != "nod_v1,nod_v3" {
		t.Errorf("unfinished video = %v, want nod_v1,nod_v3", ids)
	}
}

// TestPlayedPageUnfinishedBoundaries: the start rule keeps a place at exactly 5 s and drops one a millisecond under;
// the end rule drops a place at exactly 10 s from the end and keeps one a millisecond further back; a duration of 0
// is unknown, so only the start rule applies to it.
func TestPlayedPageUnfinishedBoundaries(t *testing.T) {
	s := openTest(t)
	seedNotify(t, s)
	if err := exec(s, `INSERT INTO nodes (id, account_id, folder_id, parent_id, name, name_key, kind, size, mime_type, created_at, modified_at, type_group)
		VALUES
		('nod_b1', 'acc_n', 'fld_n', 'nod_n', 'b1.mp4', 'b1.mp4', 'file', 0, 'video/mp4', 0, 0, 'video'),
		('nod_b2', 'acc_n', 'fld_n', 'nod_n', 'b2.mp4', 'b2.mp4', 'file', 0, 'video/mp4', 0, 0, 'video'),
		('nod_b3', 'acc_n', 'fld_n', 'nod_n', 'b3.mp4', 'b3.mp4', 'file', 0, 'video/mp4', 0, 0, 'video'),
		('nod_b4', 'acc_n', 'fld_n', 'nod_n', 'b4.mp4', 'b4.mp4', 'file', 0, 'video/mp4', 0, 0, 'video')`); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, pl := range []struct {
		node     string
		pos, dur int64
	}{
		{"nod_b1", 5000, 60000},
		{"nod_b2", 4999, 60000},
		{"nod_b3", 50000, 60000},
		{"nod_b4", 49999, 60000},
		{"nod_n", 5000, 0},
	} {
		if err := s.Write(ctx, func(tx *Tx) error {
			return tx.SetMediaResume(ctx, "acc_n", "usr_b", pl.node, MediaResume{PositionMS: pl.pos, DurationMS: pl.dur}, time.UnixMilli(1000))
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids, _ := playedIDs(t, s, "usr_b", ListFilter{}, true, Page{Limit: 10})
	kept := map[string]bool{}
	for _, id := range ids {
		kept[id] = true
	}
	for id, want := range map[string]bool{"nod_b1": true, "nod_b2": false, "nod_b3": false, "nod_b4": true, "nod_n": true} {
		if kept[id] != want {
			t.Errorf("%s kept = %v, want %v (all: %v)", id, kept[id], want, ids)
		}
	}
}

// TestPlayedPageIsPrivate: one user's places never appear in another's list, even for a file both reach.
func TestPlayedPageIsPrivate(t *testing.T) {
	s := playedFixture(t)
	ids, _ := playedIDs(t, s, "usr_b", ListFilter{}, false, Page{Limit: 10})
	for _, id := range ids {
		if id == "nod_n" {
			t.Errorf("usr_b's list holds usr_a's place: %v", ids)
		}
	}
	ids, _ = playedIDs(t, s, "usr_a", ListFilter{}, false, Page{Limit: 10})
	if strings.Join(ids, ",") != "nod_n" {
		t.Errorf("usr_a's list = %v, want only his own nod_n", ids)
	}
}

// TestPlayedPageCursorBindsFilterAndUnfinished: a cursor opens only under the filter and the unfinished flag it was
// made with; another type, another flag, or none of them refuses it.
func TestPlayedPageCursorBindsFilterAndUnfinished(t *testing.T) {
	s := playedFixture(t)
	ctx := context.Background()
	video := ListFilter{Group: "video"}
	_, rows := playedIDs(t, s, "usr_b", video, false, Page{Limit: 1})
	vc := PlayedCursorOf(rows[0], video, false)
	if _, err := s.PlayedPage(ctx, "usr_b", video, false, Page{Limit: 1, Cursor: vc}); err != nil {
		t.Errorf("the video cursor with video: %v", err)
	}
	for name, f := range map[string]ListFilter{"audio": {Group: "audio"}, "no filter": {}} {
		if _, err := s.PlayedPage(ctx, "usr_b", f, false, Page{Limit: 1, Cursor: vc}); !errors.Is(err, ErrBadCursor) {
			t.Errorf("the video cursor with %s: %v, want ErrBadCursor", name, err)
		}
	}
	if _, err := s.PlayedPage(ctx, "usr_b", video, true, Page{Limit: 1, Cursor: vc}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("the video cursor with unfinished: %v, want ErrBadCursor", err)
	}
	_, rows = playedIDs(t, s, "usr_b", ListFilter{}, true, Page{Limit: 1})
	uc := PlayedCursorOf(rows[0], ListFilter{}, true)
	if _, err := s.PlayedPage(ctx, "usr_b", ListFilter{}, true, Page{Limit: 1, Cursor: uc}); err != nil {
		t.Errorf("the unfinished cursor with unfinished: %v", err)
	}
	if _, err := s.PlayedPage(ctx, "usr_b", ListFilter{}, false, Page{Limit: 1, Cursor: uc}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("the unfinished cursor without unfinished: %v, want ErrBadCursor", err)
	}
	if _, err := s.PlayedPage(ctx, "usr_b", video, true, Page{Limit: 1, Cursor: uc}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("the unfinished cursor with video: %v, want ErrBadCursor", err)
	}
	plain := PlayedCursorOf(rows[0], ListFilter{}, false)
	if _, err := s.PlayedPage(ctx, "usr_b", video, false, Page{Limit: 1, Cursor: plain}); !errors.Is(err, ErrBadCursor) {
		t.Errorf("a plain cursor with video: %v, want ErrBadCursor", err)
	}
}

// TestPlayedPageSQLUsesThePrimaryKey: every shape of the played query reaches its rows through the primary key's
// user_id prefix, never a scan of every user's places. The plans are read on an empty database, as the other plan
// tests do: the statement is the one PlayedPage runs.
func TestPlayedPageSQLUsesThePrimaryKey(t *testing.T) {
	s := openTest(t)
	tagged := PlayedCursorOf(PlayedRow{NodeID: "nod_x", UpdatedAt: time.UnixMilli(1000)}, ListFilter{Group: "video"}, true)
	cases := []struct {
		name       string
		f          ListFilter
		unfinished bool
		p          Page
	}{
		{"no filter", ListFilter{}, false, Page{Limit: 50}},
		{"video", ListFilter{Group: "video"}, false, Page{Limit: 50}},
		{"unfinished", ListFilter{}, true, Page{Limit: 50}},
		{"video unfinished", ListFilter{Group: "video"}, true, Page{Limit: 50}},
		{"cursor", ListFilter{}, false, Page{Limit: 50, Cursor: encodeCursor("1000", "nod_x")}},
		{"video unfinished cursor", ListFilter{Group: "video"}, true, Page{Limit: 50, Cursor: tagged}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			query, args, err := playedPageSQL("usr_b", c.f, c.unfinished, c.p)
			if err != nil {
				t.Fatal(err)
			}
			plan := explainPlan(t, s, query, args...)
			if strings.Contains(plan, "SCAN media_resume") || strings.Contains(plan, "SCAN m") {
				t.Errorf("%s scans the places: %s", c.name, plan)
			}
			if !strings.Contains(plan, "sqlite_autoindex_media_resume_1") {
				t.Errorf("%s does not use the primary key: %s", c.name, plan)
			}
			t.Logf("plan: %s", plan)
		})
	}
}
