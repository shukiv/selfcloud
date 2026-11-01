// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shukiv/selfcloud/internal/office"
)

// TestCancelledConversionKeepsItsAttempt (review, decision 46): the attempt is written before the document is sent. A
// conversion cut off by a shutdown keeps it, so a document that kills the converter runs out of tries; the row stays
// pending, due at once, for the next start.
func TestCancelledConversionKeepsItsAttempt(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "cut.docx", docxBytes("cutword"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{cut: cancel}
	w := fx.worker(conv, clock)
	// The first steps may be the backfill's; the conversion is the step that reaches the converter.
	for i := 0; conv.calls == 0 && i < 100; i++ {
		if _, err := w.Step(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if conv.calls != 1 {
		t.Fatalf("requests to the converter: %d, want 1", conv.calls)
	}
	if r := rowOf(t, fx.s, n.ID); r.Attempts != 1 || r.State != "pending" {
		t.Fatalf("after a cut conversion: %+v, want one attempt and still pending", r)
	}
}

// TestUnreachableConverterCountsNoAttempt (review, decision 46): no request reached an unreachable converter, so the
// attempt written before the send is taken back, and the row stays at zero attempts.
func TestUnreachableConverterCountsNoAttempt(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "down.docx", docxBytes("downword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	conv := &fakeConverter{err: office.ErrUnreachable}
	drainText(t, fx.worker(conv, clock))
	if conv.calls != 1 {
		t.Fatalf("requests to the converter: %d, want 1", conv.calls)
	}
	if r := rowOf(t, fx.s, n.ID); r.Attempts != 0 || r.State != "pending" {
		t.Fatalf("after an unreachable converter: %+v, want no attempt and pending", r)
	}
}

// cutAttempt runs one conversion whose request is cut off by a shutdown: the context is cancelled during the request.
func cutAttempt(t *testing.T, fx *contentFixture, clock *fakeClock) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conv := &fakeConverter{cut: cancel}
	w := fx.worker(conv, clock)
	for i := 0; conv.calls == 0 && i < 100; i++ {
		if _, err := w.Step(ctx); err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if conv.calls != 1 {
		t.Fatalf("a cut attempt reached the converter %d times, want 1", conv.calls)
	}
}

// TestCutAttemptsStopAtTheCap (review, decision 46): a document whose three in-flight requests are cut off by shutdowns is
// refused at the cap on the next start, and no restart sends it again. The count stays at three; the row is refused under
// the current converter key.
func TestCutAttemptsStopAtTheCap(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "loop.docx", docxBytes("loopword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	for range 3 {
		cutAttempt(t, fx, clock)
	}
	if r := rowOf(t, fx.s, n.ID); r.Attempts != 3 || r.State != "pending" {
		t.Fatalf("after three cut attempts: %+v, want three attempts and pending", r)
	}
	// Three restarts, each with a working converter: none sends the document.
	for restart := 1; restart <= 3; restart++ {
		conv := &fakeConverter{out: []byte("loopword")}
		drainText(t, fx.worker(conv, clock))
		if conv.calls != 0 {
			t.Fatalf("restart %d sent the document to the converter %d times, want none", restart, conv.calls)
		}
	}
	r := rowOf(t, fx.s, n.ID)
	if r.Attempts != 3 || r.State != "failed" || r.Error != "the converter failed while it had the document" {
		t.Fatalf("after the restarts: %+v, want three attempts, failed and refused under the key", r)
	}
}

// TestNewConverterKeyGivesCappedDocumentsOneSetOfTries: a new converter key releases a refused document with its count
// reset, and it is sent again.
func TestNewConverterKeyGivesCappedDocumentsOneSetOfTries(t *testing.T) {
	t.Parallel()
	fx := newContentFixture(t)
	n := fx.file(t, "again.docx", docxBytes("againword"))
	clock := &fakeClock{now: time.Unix(1_000_000, 0)}
	for range 3 {
		cutAttempt(t, fx, clock)
	}
	drainText(t, fx.worker(&fakeConverter{out: []byte("againword")}, clock))
	conv := &fakeConverter{build: "build-2", out: []byte("againword")}
	drainText(t, fx.worker(conv, clock))
	if conv.calls != 1 {
		t.Fatalf("after a new converter key: %d requests, want 1", conv.calls)
	}
	if r := rowOf(t, fx.s, n.ID); r.State != "ready" {
		t.Fatalf("after a new converter key: %+v, want ready", r)
	}
}
