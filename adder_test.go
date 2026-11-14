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
)

func TestAdder(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		deltas   []int64
		expected int64
	}{
		{name: "starts at zero", deltas: nil, expected: 0},
		{name: "single add", deltas: []int64{5}, expected: 5},
		{name: "several adds", deltas: []int64{1, 2, 3, 4}, expected: 10},
		{name: "negative deltas", deltas: []int64{10, -3, -7, -1}, expected: -1},
		{name: "wraps on overflow", deltas: []int64{math.MaxInt64, 1}, expected: math.MinInt64},
		{name: "wraps back after overflow", deltas: []int64{math.MaxInt64, 1, math.MaxInt64, 1}, expected: 0},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			adder := NewAdder()
			for _, delta := range testCase.deltas {
				adder.Add(delta)
			}
			assert.Equal(t, testCase.expected, adder.Value())
		})
	}
}

func TestAdderStripeCount(t *testing.T) {
	t.Parallel()

	adder := NewAdder()
	assert.Len(t, adder.stripes, stripeCount())
	assert.Equal(t, len(adder.stripes)-1, adder.mask)
}

func TestAdderConcurrent(t *testing.T) {
	t.Parallel()

	const (
		goroutines = 32
		addsEach   = 5000
	)
	adder := NewAdder()
	var group sync.WaitGroup
	for range goroutines {
		group.Go(func() {
			for range addsEach {
				adder.Add(1)
				_ = adder.Value()
			}
		})
	}
	group.Wait()
	assert.Equal(t, int64(goroutines*addsEach), adder.Value())
}

func TestAdderHeaderFillsOneCacheLine(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uintptr(cacheLineSize), reflect.TypeFor[Adder]().Size(),
		"the Adder header must own a whole cache line so neighbours cannot falsely share it")
	assert.Equal(t, uintptr(cacheLineSize), reflect.TypeFor[adderStripe]().Size())
}
