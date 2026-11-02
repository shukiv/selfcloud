// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// audioBytes reads a test file of the audio reader (internal/audio/testdata, ffmpeg made).
func audioBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../audio/testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tagsPool is a pool on the fixture's store with the audio source limit set to max.
func (f *fixture) tagsPool(max int64) *Pool {
	return f.newPool(func(o *Options) { o.AudioMaxSourceBytes = max })
}

// tagsState returns the tags row of a blob under a pool's converter, as the pool stored it.
func (f *fixture) tagsState(p *Pool, accountID, blobID string) *store.Derived {
	f.t.Helper()
	d, err := f.cache.Lookup(f.ctx, accountID, blobID, KindTags, p.TagsConverter())
	if err != nil {
		f.t.Fatal(err)
	}
	return d
}

func TestTagsReadMarksTheRowReady(t *testing.T) {
	f := newFixture(t, 8)
	p := f.tagsPool(1 << 30)
	bl := f.putBlob(f.acc, audioBytes(t, "tag.flac"))

	ans, err := p.RequestTags(f.ctx, f.acc, bl, nil)
	if err != nil || ans.State != "pending" {
		t.Fatalf("first request: %+v %v, want pending", ans, err)
	}
	f.runOn(p)
	if d := f.tagsState(p, f.acc, bl); d == nil || d.State != "ready" {
		t.Fatalf("after the read: %+v, want ready", d)
	}
	// A ready read answers ready and queues nothing: it has no file to check, and its values are in the table.
	ans, err = p.RequestTags(f.ctx, f.acc, bl, nil)
	if err != nil || ans.State != "ready" || p.queuedJobs() != 0 {
		t.Fatalf("second request: %+v %v, queued %d; want ready, nothing queued", ans, err, p.queuedJobs())
	}
}

func TestTagsOverTheAudioLimitAreRefused(t *testing.T) {
	f := newFixture(t, 8)
	p := f.tagsPool(100) // the fixture is a few kilobytes
	bl := f.putBlob(f.acc, audioBytes(t, "tag.flac"))
	if _, err := p.RequestTags(f.ctx, f.acc, bl, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(p)
	d := f.tagsState(p, f.acc, bl)
	if d == nil || d.State != "failed" || d.Error != msgSourceSize || !d.RetryAt.IsZero() {
		t.Fatalf("over the limit: %+v, want a sticky failure with the size message", d)
	}
}

func TestTagsOfNotAudioAreRefusedStickily(t *testing.T) {
	f := newFixture(t, 8)
	p := f.tagsPool(1 << 30)
	bl := f.putBlob(f.acc, []byte(strings.Repeat("plain text, not audio. ", 200)))
	if _, err := p.RequestTags(f.ctx, f.acc, bl, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(p)
	d := f.tagsState(p, f.acc, bl)
	if d == nil || d.State != "failed" || d.Error != msgNotAudio || !d.RetryAt.IsZero() {
		t.Fatalf("not audio: %+v, want a sticky failure with the not-audio message", d)
	}
}

func TestTagsCorruptFileIsRefusedStickily(t *testing.T) {
	f := newFixture(t, 8)
	p := f.tagsPool(1 << 30)
	// A FLAC whose STREAMINFO block is too short: the reader refuses the header.
	bad := []byte("fLaC\x80\x00\x00\x10" + strings.Repeat("\x00", 16))
	bl := f.putBlob(f.acc, bad)
	if _, err := p.RequestTags(f.ctx, f.acc, bl, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(p)
	d := f.tagsState(p, f.acc, bl)
	if d == nil || d.State != "failed" || d.Error != msgTagsCorrupt {
		t.Fatalf("corrupt header: %+v, want the corrupt-tags message", d)
	}
}

// A duration out of range is given up, and the title and artist are kept: a bad length is not a bad title.
func TestCheckedTagsKeepsTextWhenDurationIsBad(t *testing.T) {
	for _, in := range []string{`{"title":"Keep","artist":"Me","duration_ms":-1}`, `{"title":"Keep","artist":"Me","duration_ms":604800001}`} {
		got, ok := checkedTags([]byte(in))
		if !ok {
			t.Fatalf("%s was refused; the title and artist should be kept", in)
		}
		if got.Title != "Keep" || got.Artist != "Me" || got.DurationMS != 0 {
			t.Fatalf("%s gave %+v, want the title and artist with duration 0", in, got)
		}
	}
}

// The child is given a small input cap in tags mode. The file is read by seeking, so the cap is for a stdin that cannot
// seek, and it must not be the audio file's own size limit.
func TestTagsModeIsGivenASmallInputCap(t *testing.T) {
	p := &Pool{o: Options{MaxPixels: 1, MemoryBytes: 1, CPUSeconds: 1, AudioMaxSourceBytes: 100 << 30}}
	args := tagsArgs(p.o)
	want := "-max-input=1048576"
	found := false
	for _, a := range args {
		if a == want {
			found = true
		}
		if strings.HasPrefix(a, "-max-input=") && a != want {
			t.Fatalf("tags mode passes %s, want %s", a, want)
		}
	}
	if !found {
		t.Fatalf("tags mode args %v have no %s", args, want)
	}
}

// A raised audio limit makes new thumbnail rows: an audio file refused under the old limit is not kept for the new one.
func TestThumbConverterKeyIncludesTheAudioLimit(t *testing.T) {
	base := Options{MaxPixels: 1, MaxSourceBytes: 5, MemoryBytes: 1, CPUSeconds: 1, MaxOutputBytes: 1, AudioMaxSourceBytes: 10}
	raised := base
	raised.AudioMaxSourceBytes = 20
	if converterFor(base) == converterFor(raised) {
		t.Fatal("raising the audio source limit kept the same converter key: refusals under the old limit would be served")
	}
}

func TestCheckedTagsRefusesWhatTheChildMustNotSend(t *testing.T) {
	good := `{"title":"Song","artist":"A","album":"B","duration_ms":1000}`
	if _, ok := checkedTags([]byte(good)); !ok {
		t.Fatalf("a good output was refused: %s", good)
	}
	bad := map[string]string{
		"not json":          `{`,
		"unknown field":     `{"title":"x","cover":"yes"}`,
		"trailing value":    `{"title":"x"} {"title":"y"}`,
		"control character": `{"title":"a\u0007b"}`,
		"bidi override":     `{"title":"a‮b"}`,
		"newline":           `{"artist":"a\nb"}`,
		"over the length":   `{"album":"` + strings.Repeat("a", 257) + `"}`,
	}
	for name, in := range bad {
		if _, ok := checkedTags([]byte(in)); ok {
			t.Errorf("%s: %s was accepted", name, in)
		}
	}
	// The zero-width joiner is kept: Arabic and Persian need it.
	if _, ok := checkedTags([]byte(`{"title":"a` + "‍" + `b"}`)); !ok {
		t.Error("a zero-width joiner was refused")
	}
}

func TestLimitWriterCutsAtTheLimit(t *testing.T) {
	var buf bytes.Buffer
	w := &limitWriter{w: &buf, max: 10}
	for i := 0; i < 5; i++ {
		if n, err := w.Write(bytes.Repeat([]byte{'x'}, 4)); n != 4 || err != nil {
			t.Fatalf("write %d: %d %v", i, n, err)
		}
	}
	// One byte past the limit is kept, so the caller can tell the output was over it.
	if buf.Len() != 11 {
		t.Fatalf("kept %d bytes, want 11", buf.Len())
	}
}
