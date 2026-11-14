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

//go:build go1.27 && !safe && !(js && wasm)

package sharded

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func pinBenchSelectors() []benchSelector {
	return []benchSelector{{
		name:     "proc-pin",
		newAdder: NewAdder,
		add:      (*Adder).Add,
		selectStripe: func() uint32 {
			processorID := runtimeProcPin()
			runtimeProcUnpin()
			return uint32(processorID)
		},
	}}
}

func TestStripeCountFollowsGOMAXPROCS(t *testing.T) {
	t.Parallel()

	assert.Equal(t, roundUpPowerOfTwo(min(runtime.GOMAXPROCS(0), maxStripeCount)), stripeCount())
}

func TestRuntimeProcPinReturnsAProcessorID(t *testing.T) {
	t.Parallel()

	processorID := runtimeProcPin()
	runtimeProcUnpin()
	assert.GreaterOrEqual(t, processorID, 0)
	assert.Less(t, processorID, runtime.GOMAXPROCS(0))
}

func TestAddWithFewerStripesThanProcessors(t *testing.T) {
	t.Parallel()

	for _, stripeTotal := range []int{1, 2} {
		adder := newAdderWithStripes(stripeTotal)
		var group sync.WaitGroup
		for range 16 {
			group.Go(func() {
				for range 2000 {
					adder.Add(1)
				}
			})
		}
		group.Wait()
		assert.Equal(t, int64(16*2000), adder.Value(), "%d stripes", stripeTotal)
	}
}

func TestAddAfterGOMAXPROCSGrows(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(previous) })

	adder := NewAdder()
	assert.Len(t, adder.stripes, 1)

	runtime.GOMAXPROCS(max(8, previous))
	const (
		goroutines = 32
		addsEach   = 2000
	)
	var group sync.WaitGroup
	for range goroutines {
		group.Go(func() {
			for range addsEach {
				adder.Add(1)
			}
		})
	}
	group.Wait()
	assert.Equal(t, int64(goroutines*addsEach), adder.Value())
}
