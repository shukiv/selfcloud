// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import (
	"testing"

	"github.com/shukiv/selfcloud/internal/store"
)

// TestMinFreeNowWinsOverMinFree: a pool given MinFreeNow asks it at each write and ignores the fixed MinFree. A pool whose
// MinFreeNow asks for more free space than any disk has makes no file; one whose MinFreeNow asks for none makes it,
// although its fixed MinFree is that impossible figure.
func TestMinFreeNowWinsOverMinFree(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	const impossible = uint64(1) << 62
	full := f.newPool(func(o *Options) { o.MinFree = 0; o.MinFreeNow = func() uint64 { return impossible } })
	roomy := f.newPool(func(o *Options) { o.MinFree = impossible; o.MinFreeNow = func() uint64 { return 0 } })

	a := f.putBlob(f.acc, samplePNG(t, 64, 64))
	if _, err := full.Request(f.ctx, f.acc, a, 64, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(full)
	if d := f.stateOn(full, f.acc, a, 64); d != nil && d.State == store.DerivedReady {
		t.Errorf("a pool whose MinFreeNow leaves no room made the thumbnail: %+v", d)
	}

	b := f.putBlob(f.acc, samplePNG(t, 80, 80))
	if _, err := roomy.Request(f.ctx, f.acc, b, 64, nil); err != nil {
		t.Fatal(err)
	}
	f.runOn(roomy)
	if d := f.stateOn(roomy, f.acc, b, 64); d == nil || d.State != store.DerivedReady {
		t.Errorf("a pool whose MinFreeNow leaves room did not make the thumbnail: %+v", d)
	}
}

// TestConvertMinFreeNowWinsOverMinFree: the conversion pool asks MinFreeNow at each write too.
func TestConvertMinFreeNowWinsOverMinFree(t *testing.T) {
	t.Parallel()
	f := newFixture(t, 8)
	const impossible = uint64(1) << 62
	conv := &fakeConv{out: []byte("%PDF-1.7 converted")}
	full := f.convPool(conv, func(o *ConvertOptions) { o.MinFree = 0; o.MinFreeNow = func() uint64 { return impossible } })
	a := f.putBlob(f.acc, []byte("first doc"))
	if _, err := f.convRequest(full, f.acc, a); err != nil {
		t.Fatal(err)
	}
	f.runConv(full)
	if d := f.convRow(f.acc, a); d == nil || d.State == store.DerivedReady {
		t.Errorf("a pool whose MinFreeNow leaves no room made the conversion: %+v", d)
	}

	roomy := f.convPool(conv, func(o *ConvertOptions) { o.MinFree = impossible; o.MinFreeNow = func() uint64 { return 0 } })
	b := f.putBlob(f.acc, []byte("second doc"))
	if _, err := f.convRequest(roomy, f.acc, b); err != nil {
		t.Fatal(err)
	}
	f.runConv(roomy)
	if d := f.convRow(f.acc, b); d == nil || d.State != store.DerivedReady {
		t.Errorf("a pool whose MinFreeNow leaves room did not make the conversion: %+v", d)
	}
}
