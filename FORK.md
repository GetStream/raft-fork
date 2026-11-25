# GetStream fork of hashicorp/raft

Base: hashicorp/raft v1.8.0 (0f543c056e, 2026-09-16).

This fork replaces the archived GetStream/raft fork (2019, e301a8373733), which Keevo pinned. This file records every local change from that fork and what happened to it.

## Carried over
- f4d558b862 snapshot/FSM deadlock. Reimplemented in applyBatch (raft.go) because v1.8.0 batches applies. See PR #1 and snapshot_deadlock_test.go.

## Already in upstream v1.8.0 (not needed)
- Pre-vote (fork 258d271 is a 2019 port; upstream added pre-vote in v1.7.0, PR #530, 181475cc5e). Pre-vote is on by default (Config.PreVoteDisabled=false).
- checkLeaderLease counts only voters (fork b6cb2f8f86; upstream 6e417090, PR #308).
- nextIndex atomic load/store (fork e08f7d22c9).
- Voter check before election (fork d2f312d7d4; upstream hasVote in runFollower).
- Bootstrap vote granting (fork 09330e4389; upstream grants when Servers is empty).

## Skipped: obsolete
- e301a83737 replState locking. Shutdown no longer ranges replState; nothing left to protect.
- 1e940bcd55 stop heartbeats while shutting down. Calls getShuttingDown, which does not exist upstream.
- f89e587dbc reject votes from outside the cluster. Duplicates upstream requestVote; does not compile on v1.8.0.
- 143fcc96ad backoff tests. Depends on the fork's backoff signature.
- 18ca1f8abc Travis Go versions. CI config is obsolete.

## Skipped: deliberately not carried (these contributed to the leader flapping)
- 5219f3c726 / f9e7c13b51 always grant newer-term votes. Removes the live-leader rejection that pre-vote relies on.
- 1600573cda / 680b9a627d ElectionTimeout shorter than HeartbeatTimeout. v1.8.0 rejects this config.
- f6d48d3789 / 35d1ddcd53 HeartbeatInterval config. Not in upstream v1.8.0. Keevo must stop setting it.
- 8756461f96 / c0d7ff7bb4 startup election delay.

## Skipped: graceful shutdown (upstream shutdown is not graceful)
- 003eafd758 / 85b47f6da5 graceful shutdown notify.
- 7d430f41cc parallel shutdown notify.
- 96ea521cd8 bump term on shutdown notify.
- 8af7123d70 log successful shutdown notify.
- 54c04b86b3 restore a check dropped by graceful shutdown.
- b68860d906 / 86e9b6d6f3 shutdown logging.

## Skipped: features not needed by Keevo
- d434007e53 / 4e2560c639 unsized snapshot streaming and restore.
- 14f8e887c2 EstimatedSize.
- 9490a5c088 dispatchLogs metrics. Upstream already has dispatchNumLogs.
- a8aaf254f6 / b75bd60c6c configurable max backoff. Upstream uses a fixed cap.
- eed3b67269 MaximumBackoff in tests.
- fd70d8b05d setLeader only when it changes. Not a bugfix.
- be3fe472be comment only.
- e23850a71a vote-request tests for the fork's behavior.

## Keevo API impact
Keevo depends on these fork-only APIs. Keevo must change when it points at this fork:
- ReplicationStats() (fork 1efba40123, 7ae96cba84). Used in cgo/raftkv/metrics.go and cgo/raftkv/management_rpc.go. Not in upstream v1.8.0.
- Config.HeartbeatInterval (fork f6d48d3789). Removed in v1.8.0.
- ElectionTimeout must be >= HeartbeatTimeout on v1.8.0. Keevo currently sets ElectionTimeout 50ms with HeartbeatTimeout 1s (cgo/raftkv/main.go), which fails validation.
- Config.Logger is hclog.Logger on v1.8.0.
