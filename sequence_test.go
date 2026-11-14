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
	"reflect"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSequenceOptions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name              string
		options           []SequenceOption
		expectedStart     uint64
		expectedBlockMask uint64
		expectError       bool
	}{
		{name: "defaults", options: nil, expectedStart: 0, expectedBlockMask: defaultSequenceBlockSize - 1},
		{name: "nil option is ignored", options: []SequenceOption{nil}, expectedStart: 0, expectedBlockMask: defaultSequenceBlockSize - 1},
		{name: "block size of one", options: []SequenceOption{WithBlockSize(1)}, expectedBlockMask: 0},
		{name: "largest block size", options: []SequenceOption{WithBlockSize(maxSequenceBlockSize)}, expectedBlockMask: maxSequenceBlockSize - 1},
		{name: "start", options: []SequenceOption{WithStart(1000)}, expectedStart: 1000, expectedBlockMask: defaultSequenceBlockSize - 1},
		{name: "last usable start", options: []SequenceOption{WithStart(math.MaxUint64 - 1)}, expectedStart: math.MaxUint64 - 1, expectedBlockMask: defaultSequenceBlockSize - 1},
		{name: "later option wins", options: []SequenceOption{WithBlockSize(3), WithBlockSize(8)}, expectedBlockMask: 7},
		{name: "zero block size", options: []SequenceOption{WithBlockSize(0)}, expectError: true},
		{name: "block size not a power of two", options: []SequenceOption{WithBlockSize(100)}, expectError: true},
		{name: "block size too large", options: []SequenceOption{WithBlockSize(maxSequenceBlockSize * 2)}, expectError: true},
		{name: "start at the reserved value", options: []SequenceOption{WithStart(math.MaxUint64)}, expectError: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			sequence, err := NewSequence(testCase.options...)
			if testCase.expectError {
				require.ErrorIs(t, err, ErrInvalidSequenceOption)
				assert.Nil(t, sequence)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, testCase.expectedStart, sequence.start)
			assert.Equal(t, testCase.expectedBlockMask, sequence.blockMask)
			assert.Equal(t, math.MaxUint64-testCase.expectedStart, sequence.capacity)
			assert.Len(t, sequence.slots, stripeCount())
			assert.Equal(t, len(sequence.slots)-1, sequence.slotMask)
		})
	}
}

func TestSequenceLayout(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uintptr(2*cacheLineSize), reflect.TypeFor[Sequence]().Size(),
		"the read-only header and the shared counter must each own a cache line")
	assert.Equal(t, uintptr(cacheLineSize), reflect.TypeFor[Sequence]().Field(6).Offset,
		"allocated must start the second cache line")
	assert.Equal(t, uintptr(cacheLineSize), reflect.TypeFor[sequenceSlot]().Size())
}

func TestSequenceRefillBoundaries(t *testing.T) {
	t.Parallel()

	for _, blockSize := range []uint64{1, 2, 4, 256} {
		sequence := newSequenceWithSlots(sequenceConfig{blockSize: blockSize, start: 10}, 1)
		for index := range 3*blockSize + 1 {
			value, ok := sequence.nextByToken()
			require.True(t, ok)
			require.Equal(t, index, value, "block size %d", blockSize)
			blocksTaken := index/blockSize + 1
			require.Equal(t, blocksTaken*blockSize, sequence.allocated.Load(),
				"block size %d: a new block is taken only when the last one is used up", blockSize)
		}
	}
}

func TestSequenceNextAddsStart(t *testing.T) {
	t.Parallel()

	sequence, err := NewSequence(WithStart(1), WithBlockSize(4))
	require.NoError(t, err)
	seen := make(map[uint64]struct{}, 100)
	for range 100 {
		value, err := sequence.Next()
		require.NoError(t, err)
		assert.GreaterOrEqual(t, value, uint64(1))
		seen[value] = struct{}{}
	}
	assert.Len(t, seen, 100)
}

func TestSequenceExhaustion(t *testing.T) {
	t.Parallel()

	const capacity = 10
	sequence := newSequenceWithSlots(sequenceConfig{blockSize: 4, start: math.MaxUint64 - capacity}, 1)
	var values []uint64
	for {
		offset, ok := sequence.nextByToken()
		if !ok {
			break
		}
		values = append(values, sequence.start+offset)
	}
	expected := make([]uint64, 0, capacity)
	for index := range uint64(capacity) {
		expected = append(expected, math.MaxUint64-capacity+index)
	}
	assert.Equal(t, expected, values)
	assert.Equal(t, uint64(capacity), sequence.allocated.Load(), "the counter must stop at capacity, never wrap")

	for range 3 {
		_, ok := sequence.nextByToken()
		assert.False(t, ok, "an exhausted sequence must stay exhausted")
	}
	assert.Equal(t, uint64(capacity), sequence.allocated.Load())
}

func TestSequenceNextReturnsExhaustedError(t *testing.T) {
	t.Parallel()

	sequence, err := NewSequence(WithStart(math.MaxUint64-1), WithBlockSize(1))
	require.NoError(t, err)
	value, err := sequence.Next()
	require.NoError(t, err)
	assert.Equal(t, uint64(math.MaxUint64-1), value)
	for range 3 {
		value, err = sequence.Next()
		require.ErrorIs(t, err, ErrSequenceExhausted)
		assert.Zero(t, value)
	}
}

func TestSequenceReserve(t *testing.T) {
	t.Parallel()

	sequence := newSequenceWithSlots(sequenceConfig{blockSize: 8, start: math.MaxUint64 - 19}, 1)
	testCases := []struct {
		expectedStart uint64
		expectedWhole bool
		expectedOK    bool
	}{
		{expectedStart: 0, expectedWhole: true, expectedOK: true},
		{expectedStart: 8, expectedWhole: true, expectedOK: true},
		{expectedStart: 16, expectedWhole: false, expectedOK: true},
		{expectedStart: 17, expectedWhole: false, expectedOK: true},
		{expectedStart: 18, expectedWhole: false, expectedOK: true},
		{expectedStart: 0, expectedWhole: false, expectedOK: false},
	}
	for index, testCase := range testCases {
		blockStart, whole, ok := sequence.reserve()
		assert.Equal(t, testCase.expectedStart, blockStart, "reserve %d", index)
		assert.Equal(t, testCase.expectedWhole, whole, "reserve %d", index)
		assert.Equal(t, testCase.expectedOK, ok, "reserve %d", index)
	}
}

func collectSequence(t *testing.T, goroutines, callsEach int, next func() (uint64, bool)) map[uint64]struct{} {
	t.Helper()
	results := make([][]uint64, goroutines)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range goroutines {
		group.Go(func() {
			values := make([]uint64, 0, callsEach)
			<-start
			for range callsEach {
				value, ok := next()
				if !ok {
					break
				}
				values = append(values, value)
			}
			results[index] = values
		})
	}
	close(start)
	group.Wait()

	seen := make(map[uint64]struct{}, goroutines*callsEach)
	for _, values := range results {
		for _, value := range values {
			if _, duplicate := seen[value]; duplicate {
				t.Fatalf("value %d was handed out twice", value)
			}
			seen[value] = struct{}{}
		}
	}
	return seen
}

func TestSequenceUniqueConcurrent(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 64
		callsEach  = 5000
	)
	testCases := []struct {
		name      string
		slotTotal int
		blockSize uint64
		byToken   bool
	}{
		{name: "default slots", slotTotal: stripeCount(), blockSize: defaultSequenceBlockSize},
		{name: "default slots block of one", slotTotal: stripeCount(), blockSize: 1},
		{name: "one shared slot", slotTotal: 1, blockSize: 16},
		{name: "two shared slots", slotTotal: 2, blockSize: 2},
		{name: "token default slots", slotTotal: stripeCount(), blockSize: defaultSequenceBlockSize, byToken: true},
		{name: "token one shared slot", slotTotal: 1, blockSize: 16, byToken: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			sequence := newSequenceWithSlots(sequenceConfig{blockSize: testCase.blockSize, start: 0}, testCase.slotTotal)
			next := sequence.nextFromCurrentSlot
			if testCase.byToken {
				next = sequence.nextByToken
			}
			seen := collectSequence(t, goroutines, callsEach, next)
			assert.Len(t, seen, goroutines*callsEach)
		})
	}
}

func TestSequenceNextConcurrent(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 32
		callsEach  = 5000
	)
	sequence, err := NewSequence(WithStart(7))
	require.NoError(t, err)
	seen := collectSequence(t, goroutines, callsEach, func() (uint64, bool) {
		value, err := sequence.Next()
		return value, err == nil
	})
	assert.Len(t, seen, goroutines*callsEach)
	for value := range seen {
		require.GreaterOrEqual(t, value, uint64(7))
	}
}

func TestSequenceUniqueNearExhaustion(t *testing.T) {
	t.Parallel()

	const capacity = 20_000
	for _, slotTotal := range []int{1, stripeCount()} {
		sequence := newSequenceWithSlots(sequenceConfig{blockSize: 16, start: math.MaxUint64 - capacity}, slotTotal)
		seen := collectSequence(t, 32, capacity, func() (uint64, bool) {
			offset, ok := sequence.nextFromCurrentSlot()
			return sequence.start + offset, ok
		})
		assert.LessOrEqual(t, len(seen), capacity, "%d slots", slotTotal)
		assert.Positive(t, len(seen), "%d slots", slotTotal)
		for value := range seen {
			require.GreaterOrEqual(t, value, uint64(math.MaxUint64-capacity))
			require.Less(t, value, uint64(math.MaxUint64))
		}
		assert.Equal(t, uint64(capacity), sequence.allocated.Load())
	}
}

func TestSequenceUniqueWhileGOMAXPROCSChanges(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	sequence, err := NewSequence(WithBlockSize(8))
	require.NoError(t, err)
	require.Len(t, sequence.slots, 1)
	runtime.GOMAXPROCS(max(8, previous))

	const (
		goroutines = 32
		callsEach  = 5000
	)
	stop := make(chan struct{})
	changed := make(chan struct{})
	go func() {
		defer close(changed)
		for round := 0; ; round++ {
			select {
			case <-stop:
				return
			default:
			}
			runtime.GOMAXPROCS(1 + round%max(8, previous))
			runtime.Gosched()
		}
	}()
	seen := collectSequence(t, goroutines, callsEach, func() (uint64, bool) {
		value, err := sequence.Next()
		return value, err == nil
	})
	close(stop)
	<-changed
	assert.Len(t, seen, goroutines*callsEach)
}
