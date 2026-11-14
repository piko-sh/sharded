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

// Adder is a striped int64 counter for totals that many goroutines update at the same
// time.
//
// Each stripe (shard) sits on its own cache line, and concurrent adds from different
// processors go to different stripes, so they rarely touch the same line. Value sums the
// stripes, so a read costs one load per stripe.
//
// Every add is retained. Value is exact after concurrent updates finish. An add that
// overlaps the read may or may not be included.
//
// The total wraps around on int64 overflow (two's complement), in the same way as
// atomic.Int64. Because addition modulo 2^64 is order independent, the wrapped total is
// still the true total modulo 2^64.
//
// An Adder costs about one cache line per stripe, so it suits a small number of shared
// totals, not one counter per cache entry. The header is padded to a cache line so
// neighbouring writes do not disturb every Add. Use Counter for per-item counts. An Adder
// must not be copied after first use.
type Adder struct {
	// stripes holds one padded counter per stripe.
	stripes []adderStripe

	// mask is the stripe count minus one. The count is a power of two.
	mask int

	_ [adderHeaderPadding]byte
}

// adderStripe is one cache-line padded part of an Adder.
type adderStripe struct {
	// value holds this stripe's share of the total.
	value atomic.Int64

	_ [cacheLineSize - 8]byte
}

// NewAdder returns an Adder sized for this process.
//
// There is one stripe for each processor that can run Go code at the same time, rounded
// up to a power of two and capped at 4096.
//
// Returns *Adder which starts at zero.
func NewAdder() *Adder {
	return newAdderWithStripes(stripeCount())
}

// Add adds delta to the total.
//
// Takes delta (int64) which is the amount to add and may be negative.
func (a *Adder) Add(delta int64) {
	a.addToCurrentStripe(delta)
}

// Value returns the sum of all stripes.
//
// It is exact after concurrent updates finish. An add that overlaps Value may or may not
// be included. A later Value sees that add.
//
// Returns int64 which is the total, wrapped on overflow.
func (a *Adder) Value() int64 {
	var total int64
	for index := range a.stripes {
		total += a.stripes[index].value.Load()
	}
	return total
}

// newAdderWithStripes returns an Adder with stripeTotal stripes.
//
// Takes stripeTotal (int) which must be a power of two.
//
// Returns *Adder which starts at zero.
func newAdderWithStripes(stripeTotal int) *Adder {
	return &Adder{
		stripes: make([]adderStripe, stripeTotal),
		mask:    stripeTotal - 1,
	}
}
