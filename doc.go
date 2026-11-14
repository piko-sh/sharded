// Copyright 2026 PolitePixels Limited
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This project stands against fascism, authoritarianism, and all forms of
// oppression. We built this to empower people, not to enable those who would
// strip others of their rights and dignity.

// Package sharded provides counters, id sequences and other shared values that stay fast
// when many goroutines update them at once, by giving each Go processor (P) its own
// shard.
//
// A single atomic word that every goroutine writes moves its cache line between cores on
// each write, so throughput falls as cores are added. The types here give each P its own
// shard (a stripe, padded to a cache line), so concurrent writes rarely share a line, and
// they sum the shards when read.
//
//   - [Adder] spreads one total over stripes. It suits a few shared totals, such as cache
//     hits and misses. Each Adder costs one cache line per stripe and one stripe per P,
//     which is too much to keep per item.
//   - [Counter] is one word until it sees contention, then switches to an Adder. It suits
//     per item counts, such as hits on each cache entry, when only some items are hot. A
//     cold Counter uses 16 bytes.
//   - [Sequence] hands out unique uint64 values, such as ids. Each P takes a block of
//     values from one shared counter and hands them out from its own slot, so the shared
//     counter is written once per block instead of once per value.
//
// # Semantics
//
// Every Add is applied exactly once with an atomic operation. Value is exact after
// concurrent updates finish. An add that overlaps Value may or may not be included. A
// reader racing with writers can therefore see a value that was never the total at any
// single instant. Totals wrap around on int64 overflow, like [sync/atomic.Int64].
//
// A Sequence never returns the same value twice, by any goroutine, for as long as it
// lives, in both builds and after GOMAXPROCS changes. Its values are not in global order.
// Only values from one block come out in increasing order. There are gaps, because the
// unused rest of a block is never handed out by anyone else and math.MaxUint64 is
// reserved. The range never wraps. Once it is used up, Next returns
// [ErrSequenceExhausted] instead of a value it returned before.
//
// # When to use sharded
//
//   - Write-heavy counters updated by many goroutines on many cores, where an atomic add
//     becomes the bottleneck.
//   - Totals that are read far less often than they are written.
//   - Unique ids taken by many goroutines at a high rate, where the ids only need to be
//     distinct.
//
// # When to use another type
//
//   - Values read on every operation, or read as often as written. Each read sums every
//     stripe.
//   - Code that needs compare and swap or an exact snapshot while writers run.
//   - Code with little contention, where one atomic word avoids the cost of choosing a
//     stripe or slot.
//   - Ids that must sort by the time they were made, must have no gaps, or must be unique
//     across processes or restarts. Use an atomic.Uint64 or an external id source.
//
// # Choosing a stripe
//
// In the default build, Add pins the goroutine to its P (logical processor) through
// runtime.procPin, adds to the stripe of that P, and unpins. Stripes are sized by
// GOMAXPROCS, so each P owns a stripe and writers never wait for each other. GOMAXPROCS
// can grow while the process runs, so P ids are masked onto the stripes. Two Ps may then
// share a stripe, which costs speed but never correctness. The pin covers only the single
// atomic add, because the runtime cannot preempt a pinned goroutine.
//
// A Sequence picks its slot in the same way. Every change to a slot is a compare and
// swap, so two Ps that share a slot after GOMAXPROCS grows can never take the same value.
// One of them tries again.
//
// runtime.procPin is reached through go:linkname. If a future Go release forbids that
// link, build with -tags safe. The safe build, which is also used for GOOS=js
// GOARCH=wasm, drops the runtime link. Stripes are then chosen per P through a sync.Pool
// token, with compare and swap and a move to another stripe on contention. A Sequence
// keeps its blocks in its own slots in both builds, so a token lost to the garbage
// collector never loses a block. No type starts a goroutine.
//
// # Thread safety
//
// All methods are safe for concurrent use. Values must not be copied after first use. The
// zero value of Counter is ready to use. An Adder must be created with [NewAdder] and a
// Sequence with [NewSequence].
package sharded
