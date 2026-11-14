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

//go:build safe || (js && wasm)

package sharded

import (
	"runtime"
)

// addToCurrentStripe adds delta to the stripe last used on the current P (logical
// processor), found through a sync.Pool token.
//
// Takes delta (int64) which is the amount to add.
func (a *Adder) addToCurrentStripe(delta int64) {
	a.addByToken(delta)
}

// stripeCount returns the number of usable processors rounded up to a power of two and
// capped at maxStripeCount.
//
// Returns int which is a power of two no larger than maxStripeCount.
func stripeCount() int {
	return roundUpPowerOfTwo(min(runtime.GOMAXPROCS(0), runtime.NumCPU(), maxStripeCount))
}
