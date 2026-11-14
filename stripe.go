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

const (
	// cacheLineSize is the assumed size of a CPU cache line in bytes.
	cacheLineSize = 64

	// adderHeaderPadding brings the Adder header (a slice header and an int mask, 32 bytes)
	// up to one whole cache line.
	adderHeaderPadding = cacheLineSize - 32

	// maxStripeCount caps every stripe count in the package so that a machine with very many
	// cores cannot make one value use a large amount of memory.
	maxStripeCount = 1 << 12
)

// roundUpPowerOfTwo returns the smallest power of two that is at least value, or 1 when
// value is below 2.
//
// Takes value (int) which callers keep at or below maxStripeCount.
//
// Returns int which is a power of two.
func roundUpPowerOfTwo(value int) int {
	result := 1
	for result < value {
		result <<= 1
	}
	return result
}
