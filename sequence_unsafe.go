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

// nextFromCurrentSlot takes the next offset from the slot owned by the current P (logical
// processor).
//
// Returns uint64 which is the offset of the value from the start.
// Returns bool which is false when the range is used up.
func (s *Sequence) nextFromCurrentSlot() (uint64, bool) {
	processorID := runtimeProcPin()
	slot := &s.slots[processorID&s.slotMask]
	for {
		offset, outcome := s.takeFromSlot(slot)
		if outcome != slotContended {
			runtimeProcUnpin()
			return offset, outcome == slotTaken
		}
	}
}
