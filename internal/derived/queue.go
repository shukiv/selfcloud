// SPDX-License-Identifier: AGPL-3.0-or-later

package derived

import "sync"

// queue holds the jobs waiting for a worker. It is round-robin across accounts: each account with work waits in
// turn, so one account's backlog does not hold the workers while another account waits (design decision 33).
//
// A request reserves a place before it writes anything. A reservation counts against the total and against its
// account until the job is popped or the reservation is released, so a request that finds the queue full costs no
// writer lock, and a reserved request that fails after reserving releases the place without a compensating write.
type queue struct {
	mu         sync.Mutex
	limit      int            // most reservations and queued jobs at once, over all accounts
	perAccount int            // most reservations and queued jobs of one account
	total      int            // reservations plus queued jobs
	counts     map[string]int // reservations plus queued jobs, per account
	jobs       map[string][]job
	active     []string // accounts with queued jobs, in the order they are served
	// ready holds one token per queued job. Its capacity is the limit, which is never exceeded, so a push never waits.
	ready chan struct{}
}

func newQueue(limit, perAccount int) *queue {
	return &queue{
		limit:      limit,
		perAccount: perAccount,
		counts:     map[string]int{},
		jobs:       map[string][]job{},
		ready:      make(chan struct{}, limit),
	}
}

// reserve takes a place for an account. It reports false, and takes nothing, when the total or the account's share is full.
func (q *queue) reserve(acc string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.total >= q.limit || q.counts[acc] >= q.perAccount {
		return false
	}
	q.total++
	q.counts[acc]++
	return true
}

// release gives back a place that was reserved and not pushed.
func (q *queue) release(acc string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.total--
	q.counts[acc]--
	if q.counts[acc] == 0 {
		delete(q.counts, acc)
	}
}

// push queues a job for a reserved place. The place stays counted until the job is popped.
func (q *queue) push(acc string, j job) {
	q.mu.Lock()
	if len(q.jobs[acc]) == 0 {
		q.active = append(q.active, acc)
	}
	q.jobs[acc] = append(q.jobs[acc], j)
	q.mu.Unlock()
	q.ready <- struct{}{}
}

// pop takes the next job in turn. It is called once for each token received from ready.
func (q *queue) pop() (job, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.active) == 0 {
		return job{}, false
	}
	acc := q.active[0]
	q.active = q.active[1:]
	list := q.jobs[acc]
	j := list[0]
	if len(list) == 1 {
		delete(q.jobs, acc)
	} else {
		q.jobs[acc] = list[1:]
		q.active = append(q.active, acc)
	}
	q.total--
	q.counts[acc]--
	if q.counts[acc] == 0 {
		delete(q.counts, acc)
	}
	return j, true
}
