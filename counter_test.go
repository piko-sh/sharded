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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCounter(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		deltas   []int64
		expected int64
		inflate  bool
	}{
		{name: "zero value reads zero", deltas: nil, expected: 0},
		{name: "single add", deltas: []int64{7}, expected: 7},
		{name: "negative deltas", deltas: []int64{3, -5}, expected: -2},
		{name: "wraps on overflow", deltas: []int64{math.MaxInt64, 1}, expected: math.MinInt64},
		{name: "inflated keeps base", deltas: []int64{4, 6}, expected: 10, inflate: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var counter Counter
			for index, delta := range testCase.deltas {
				if testCase.inflate && index == len(testCase.deltas)-1 {
					counter.inflate()
				}
				counter.Add(delta)
			}
			assert.Equal(t, testCase.expected, counter.Value())
			assert.Equal(t, testCase.inflate, counter.inflated())
		})
	}
}

func TestCounterSizeBeforeInflation(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uintptr(16), reflect.TypeFor[Counter]().Size(), "a cold Counter is two words")
}

func TestCounterStaysSmallWithoutContention(t *testing.T) {
	t.Parallel()

	var counter Counter
	for range 10000 {
		counter.Add(1)
	}
	assert.False(t, counter.inflated())
	assert.Equal(t, int64(10000), counter.Value())
}

func TestCounterInflateInstallsOneAdder(t *testing.T) {
	t.Parallel()

	var counter Counter
	first := counter.inflate()
	second := counter.inflate()
	require.NotNil(t, first)
	assert.Same(t, first, second)
}

func TestCounterInflateAllocatesNothingOnceInflated(t *testing.T) {
	var counter Counter
	counter.inflate()
	allocations := testing.AllocsPerRun(100, func() {
		counter.inflate()
	})
	assert.Zero(t, allocations, "goroutines that arrive after inflation must reuse the installed Adder")
}

func TestCounterInflateRace(t *testing.T) {
	t.Parallel()

	const goroutines = 16
	var counter Counter
	adders := make([]*Adder, goroutines)
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range goroutines {
		group.Go(func() {
			<-start
			adders[index] = counter.inflate()
		})
	}
	close(start)
	group.Wait()
	for _, adder := range adders {
		assert.Same(t, adders[0], adder)
	}
}

func TestCounterConcurrent(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 32
		addsEach   = 5000
	)
	var counter Counter
	start := make(chan struct{})
	var group sync.WaitGroup
	for index := range goroutines {
		group.Go(func() {
			<-start
			for add := range addsEach {
				if index == 0 && add == addsEach/2 {
					counter.inflate()
				}
				counter.Add(1)
				_ = counter.Value()
			}
		})
	}
	close(start)
	group.Wait()

	require.True(t, counter.inflated(), "the counter must have inflated during the run")
	baseCount := counter.base.Load()
	stripedCount := counter.striped.Load().Value()
	assert.Positive(t, baseCount, "adds before inflation stay in the base word")
	assert.Positive(t, stripedCount, "adds after inflation go to the stripes")
	assert.Equal(t, int64(goroutines*addsEach), baseCount+stripedCount)
	assert.Equal(t, int64(goroutines*addsEach), counter.Value())
}
