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
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRoundUpPowerOfTwo(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		value    int
		expected int
	}{
		{name: "negative becomes one", value: -1, expected: 1},
		{name: "zero becomes one", value: 0, expected: 1},
		{name: "one stays one", value: 1, expected: 1},
		{name: "two stays two", value: 2, expected: 2},
		{name: "three becomes four", value: 3, expected: 4},
		{name: "power of two is kept", value: 64, expected: 64},
		{name: "just above a power of two", value: 65, expected: 128},
		{name: "largest stripe count", value: maxStripeCount, expected: maxStripeCount},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, testCase.expected, roundUpPowerOfTwo(testCase.value))
		})
	}
}

func TestStripeCount(t *testing.T) {
	t.Parallel()

	count := stripeCount()
	assert.GreaterOrEqual(t, count, 1)
	assert.LessOrEqual(t, count, maxStripeCount)
	assert.Zero(t, count&(count-1), "stripe count must be a power of two")
}
