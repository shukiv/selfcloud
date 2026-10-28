// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"context"
	"errors"
	"sync"
)

// ErrFake is returned by a Recorder whose Fail is set.
var ErrFake = errors.New("mail: the fake sender was told to fail")

// Recorder is a Sender that keeps the messages it is given instead of sending them. Tests use it.
type Recorder struct {
	// Fail makes every Send return ErrFake without recording the message.
	Fail bool

	mu   sync.Mutex
	msgs []Message
}

// Send records m.
func (r *Recorder) Send(ctx context.Context, m Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.Fail {
		return ErrFake
	}
	r.msgs = append(r.msgs, m)
	return nil
}

// Messages returns the messages recorded so far, oldest first.
func (r *Recorder) Messages() []Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Message(nil), r.msgs...)
}
