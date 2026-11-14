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
	"sync"
	"sync/atomic"
)

const (
	// seedIncrement spreads new token seeds over the uint32 range. It is the 32 bit golden
	// ratio constant used for the same job by Java's ThreadLocalRandom probe.
	seedIncrement = 0x9e3779b9

	// xorshiftLeftFirst is the first shift of a 32 bit xorshift generator.
	xorshiftLeftFirst = 13

	// xorshiftRight is the right shift of the generator.
	xorshiftRight = 17

	// xorshiftLeftSecond is the final left shift of the generator.
	xorshiftLeftSecond = 5
)

var (
	// tokenPool holds stripe tokens.
	tokenPool sync.Pool

	// seedSequence hands out a distinct starting index to each new token.
	seedSequence atomic.Uint32
)

// stripeToken remembers which stripe the current P last used.
type stripeToken struct {
	// index selects a stripe after masking with the stripe count.
	index uint32

	_ [cacheLineSize - 4]byte
}

// rehash moves the token to another stripe after contention.
func (t *stripeToken) rehash() {
	t.index = xorshift32(t.index)
}

// addByToken adds delta to the stripe last used on the current P.
//
// Takes delta (int64) which is the amount to add.
func (a *Adder) addByToken(delta int64) {
	token := acquireToken()
	for {
		stripe := &a.stripes[int(token.index)&a.mask]
		current := stripe.value.Load()
		if stripe.value.CompareAndSwap(current, current+delta) {
			break
		}
		token.rehash()
	}
	releaseToken(token)
}

// acquireToken returns a stripe token for the current P.
//
// The caller must hand it back with releaseToken.
//
// Returns *stripeToken which is never nil.
func acquireToken() *stripeToken {
	if token, ok := tokenPool.Get().(*stripeToken); ok {
		return token
	}
	return &stripeToken{index: newSeed()}
}

// releaseToken returns a token to the pool for reuse on this P.
//
// Takes token (*stripeToken) which the caller must not use afterwards.
func releaseToken(token *stripeToken) {
	tokenPool.Put(token)
}

// xorshift32 returns the next state of a 32 bit xorshift generator. A non-zero state
// never becomes zero.
//
// Takes value (uint32) which is the current state.
//
// Returns uint32 which is the next state.
func xorshift32(value uint32) uint32 {
	value ^= value << xorshiftLeftFirst
	value ^= value >> xorshiftRight
	value ^= value << xorshiftLeftSecond
	return value
}

// newSeed returns a non-zero starting index for a new token.
//
// Returns uint32 which is never zero.
func newSeed() uint32 {
	seed := seedSequence.Add(seedIncrement)
	if seed == 0 {
		return seedIncrement
	}
	return seed
}
