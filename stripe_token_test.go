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
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripeTokenRehash(t *testing.T) {
	t.Parallel()

	token := &stripeToken{index: 1}
	seen := make(map[uint32]struct{}, 1000)
	for range 1000 {
		token.rehash()
		require.NotZero(t, token.index, "xorshift must never reach zero")
		seen[token.index] = struct{}{}
	}
	assert.Len(t, seen, 1000, "xorshift must not repeat within a short run")
}

func TestXorshift32(t *testing.T) {
	t.Parallel()

	assert.Zero(t, xorshift32(0), "zero is the fixed point, so callers must avoid it")
	assert.NotZero(t, xorshift32(1))
	assert.NotEqual(t, uint32(1), xorshift32(1))
}

func TestNewSeed(t *testing.T) {
	t.Parallel()

	first := newSeed()
	second := newSeed()
	assert.NotZero(t, first)
	assert.NotZero(t, second)
	assert.NotEqual(t, first, second)
}

func TestNewSeedSkipsZero(t *testing.T) {
	seedSequence.Store(math.MaxUint32 - seedIncrement + 1)
	assert.Equal(t, uint32(seedIncrement), newSeed())
}

func TestAcquireTokenReturnsSeededToken(t *testing.T) {
	t.Parallel()

	token := acquireToken()
	require.NotNil(t, token)
	assert.NotZero(t, token.index)
	releaseToken(token)
}

func TestAddByTokenConcurrent(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 16
		addsEach   = 5000
	)
	for _, stripeTotal := range []int{1, 4} {
		adder := newAdderWithStripes(stripeTotal)
		var group sync.WaitGroup
		for range goroutines {
			group.Go(func() {
				for range addsEach {
					adder.addByToken(1)
				}
			})
		}
		group.Wait()
		adder.addByToken(-1)
		assert.Equal(t, int64(goroutines*addsEach-1), adder.Value(), "%d stripes", stripeTotal)
	}
}
