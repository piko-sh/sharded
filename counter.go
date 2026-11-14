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

package sharded

import (
	"sync/atomic"
)

const (
	// inflationThreshold is the number of failed compare and swap attempts in one Add after
	// which a Counter switches to stripes.
	//
	// Four keeps cold counters small and lets a hot counter inflate quickly.
	inflationThreshold = 4

	// counterBytes is the size of a Counter before it inflates: the count and the stripe
	// pointer.
	counterBytes = 16
)

// Counter is an exact int64 counter that starts as a single word and moves to stripes
// only when it is contended.
//
// Add uses compare and swap on one word, and after inflationThreshold failures in one
// call it installs an Adder and sends all later adds there. Cold counters stay small.
// Only hot counters pay for stripes, and the stripes are never removed.
//
// The zero value is ready to use. A Counter uses 16 bytes until it inflates. Go cannot
// tag a pointer inside an integer safely, so the count and the stripe pointer are two
// words instead of one. After inflation it also holds one Adder.
//
// Every add is retained. Value is exact after concurrent updates finish. An add that
// overlaps the read may or may not be included.
//
// The total wraps around on int64 overflow, like Adder. A Counter must not be copied
// after first use.
type Counter struct {
	// striped holds the stripes once the counter has inflated, or nil.
	striped atomic.Pointer[Adder]

	// base holds the count added before inflation, and any adds that loaded a nil striped
	// pointer just before inflation.
	base atomic.Int64
}

// Add adds delta to the counter.
//
// Takes delta (int64) which is the amount to add and may be negative.
func (c *Counter) Add(delta int64) {
	if adder := c.striped.Load(); adder != nil {
		adder.Add(delta)
		return
	}
	for range inflationThreshold {
		current := c.base.Load()
		if c.base.CompareAndSwap(current, current+delta) {
			return
		}
	}
	c.inflate().Add(delta)
}

// Value returns the current count.
//
// It is exact after concurrent updates finish. An add that overlaps Value may or may not
// be included. A later Value sees that add.
//
// Returns int64 which is the total, wrapped on overflow.
func (c *Counter) Value() int64 {
	total := c.base.Load()
	if adder := c.striped.Load(); adder != nil {
		total += adder.Value()
	}
	return total
}

// inflated reports whether the counter has switched to stripes.
//
// Returns bool which is true once an Adder is installed.
func (c *Counter) inflated() bool {
	return c.striped.Load() != nil
}

// inflate installs an Adder if none is present yet.
//
// It checks for an installed Adder before building one, so goroutines that arrive after
// inflation allocate nothing. When two goroutines still race to install, the loser drops
// its Adder and uses the winner's, so exactly one Adder is ever installed.
//
// Returns *Adder which is the installed Adder.
func (c *Counter) inflate() *Adder {
	if existing := c.striped.Load(); existing != nil {
		return existing
	}
	adder := NewAdder()
	if c.striped.CompareAndSwap(nil, adder) {
		return adder
	}
	return c.striped.Load()
}

// InflatedCounterBytes returns the memory, in bytes, that one Counter uses in this
// process once it has inflated: the Counter itself, one Adder header and one cache line
// for each stripe NewAdder would create now.
//
// Returns int which is the size in bytes.
func InflatedCounterBytes() int {
	return counterBytes + cacheLineSize + stripeCount()*cacheLineSize
}
