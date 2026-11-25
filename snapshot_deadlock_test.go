// Copyright IBM Corp. 2013, 2026
// SPDX-License-Identifier: MPL-2.0

package raft

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingApplyFSM is a test FSM whose Apply blocks until the test releases it.
// This lets the test park the FSM goroutine inside an application so the main
// loop is forced to wait when it tries to hand the next committed batch to the
// FSM over fsmMutateCh. Snapshot and Restore are trivial and never block.
type blockingApplyFSM struct {
	mu   sync.Mutex
	logs [][]byte

	// gate blocks every Apply until the test closes it.
	gate chan struct{}
	// entered is closed the first time Apply is called, so the test knows the
	// FSM goroutine is now parked inside Apply.
	entered     chan struct{}
	enteredOnce sync.Once
}

func newBlockingApplyFSM() *blockingApplyFSM {
	return &blockingApplyFSM{
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
}

func (f *blockingApplyFSM) Apply(log *Log) interface{} {
	f.enteredOnce.Do(func() { close(f.entered) })
	<-f.gate
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, log.Data)
	return len(f.logs)
}

func (f *blockingApplyFSM) Snapshot() (FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &MockSnapshot{f.logs, len(f.logs)}, nil
}

func (f *blockingApplyFSM) Restore(rc io.ReadCloser) error {
	return rc.Close()
}

// TestRaft_SnapshotApplyDeadlock reproduces the deadlock between a snapshot and
// a concurrent apply batch on a running leader.
//
// takeSnapshot holds the FSM snapshot and then sends a configurationsFuture on
// configurationsCh, blocking on its Error() until the main loop answers it (see
// snapshot.go). The main loop, meanwhile, can be parked inside applyBatch
// waiting to deliver a committed batch to a busy FSM goroutine over
// fsmMutateCh. Before the fix, applyBatch selected only on fsmMutateCh and
// shutdownCh, so while it was parked the main loop never served
// configurationsCh and the snapshot's future was never answered: a running
// leader deadlocked. With the fix, applyBatch also serves configurationsCh
// while waiting, so the configuration future completes and both sides proceed.
//
// The test drives this window deterministically. It blocks the FSM inside
// Apply, lets the main loop park trying to deliver the next batch, then sends
// the exact configurationsFuture that takeSnapshot sends and asserts it is
// answered before a timeout. Without the fix this times out; with it, it
// passes.
func TestRaft_SnapshotApplyDeadlock(t *testing.T) {
	fsm := newBlockingApplyFSM()

	conf := inmemConfig(t)
	// We drive everything by hand; keep automatic snapshots out of the way.
	conf.SnapshotInterval = time.Hour
	conf.SnapshotThreshold = 1 << 62
	// One log per FSM batch so that each committed Apply is its own send on
	// fsmMutateCh. With the FSM parked on the first Apply, a few hundred applies
	// then overflow the fsmMutateCh buffer (cap 128) and force the main loop to
	// park inside applyBatch on the send.
	conf.MaxAppendEntries = 1

	c := MakeClusterCustom(t, &MakeClusterOpts{
		Peers:     1,
		Bootstrap: true,
		Conf:      conf,
		MakeFSMFunc: func() FSM {
			return fsm
		},
	})
	defer c.Close()

	leader := c.Leader()

	// Submit many applies from background goroutines. On a single-node leader
	// each commits one at a time, so each becomes its own batch sent on
	// fsmMutateCh. The first parks the FSM goroutine inside Apply (on the gate),
	// so the FSM stops draining fsmMutateCh and the remaining batches fill its
	// buffer (cap 128). Once the buffer is full, the main loop parks inside
	// applyBatch on the fsmMutateCh send, which is the window in which it must
	// still serve configurationsCh. We submit from goroutines because Apply
	// itself blocks on applyCh once the main loop stops draining it, and we must
	// not block the test goroutine before it can issue the configuration request.
	const numApplies = 400
	var applyWG sync.WaitGroup
	applyErrs := make([]error, numApplies)
	for i := 0; i < numApplies; i++ {
		applyWG.Add(1)
		go func(i int) {
			defer applyWG.Done()
			applyErrs[i] = leader.Apply([]byte{byte(i)}, 0).Error()
		}(i)
	}

	// Wait until the FSM goroutine is actually parked inside Apply.
	select {
	case <-fsm.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the FSM to start applying")
	}

	// Give the main loop time to commit entries, fill the fsmMutateCh buffer and
	// park inside applyBatch on the fsmMutateCh send against the busy FSM.
	time.Sleep(500 * time.Millisecond)

	// Send the exact configuration request that takeSnapshot issues while it is
	// holding the FSM snapshot. Only the main loop can answer it.
	configReq := &configurationsFuture{}
	configReq.ShutdownCh = leader.shutdownCh
	configReq.init()

	configDone := make(chan error, 1)
	go func() {
		select {
		case leader.configurationsCh <- configReq:
		case <-leader.shutdownCh:
			configDone <- ErrRaftShutdown
			return
		}
		configDone <- configReq.Error()
	}()

	// The snapshot's configuration future must be answered even though the main
	// loop is parked waiting to hand the batch to the busy FSM. Give it a short
	// window to prove it is served while still parked, before we release Apply.
	select {
	case err := <-configDone:
		// Served while the FSM was still blocked: the fix works.
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		// Not served while parked. Release Apply so we don't hang the suite,
		// then fail: this is the deadlock the fix addresses.
		close(fsm.gate)
		t.Fatal("deadlock: configuration request not served while applying a batch")
	}

	// Release the FSM so the pending applies drain.
	close(fsm.gate)

	applyWG.Wait()
	for i, err := range applyErrs {
		require.NoErrorf(t, err, "apply %d failed", i)
	}
}
