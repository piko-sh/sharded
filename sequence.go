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
	"errors"
	"fmt"
	"math"
	"sync/atomic"
)

const (
	// defaultSequenceBlockSize is the number of values a slot takes from the shared range at
	// a time when no block size is given.
	defaultSequenceBlockSize = 256

	// maxSequenceBlockSize caps the block size so that unused parts of blocks cannot use up
	// a large share of the range.
	maxSequenceBlockSize = 1 << 20

	// sequenceHeaderPadding brings the read-only part of the Sequence (a slice header and
	// four 8 byte words, 56 bytes) up to one cache line.
	sequenceHeaderPadding = cacheLineSize - 56
)

const (
	// slotTaken means takeFromSlot handed out a value.
	slotTaken slotOutcome = iota

	// slotContended means another goroutine changed the slot first, so the caller should try
	// again.
	slotContended

	// slotExhausted means the slot is used up and the shared range has no values left.
	slotExhausted
)

var (
	// ErrSequenceExhausted is returned by Sequence.Next when every value of the range has
	// been handed out or left in a block.
	ErrSequenceExhausted = errors.New("sharded: sequence exhausted")

	// ErrInvalidSequenceOption is returned by NewSequence when an option is out of range.
	ErrInvalidSequenceOption = errors.New("sharded: invalid sequence option")
)

// Sequence hands out unique uint64 values, such as ids, to many goroutines at the same
// time.
//
// Each slot (one per processor, on its own cache line) takes a block of values from a
// shared range and hands them out one by one, so the shared word is written once per
// block instead of once per value.
//
// Every value is handed out at most once for the life of the Sequence, by any goroutine.
// Values are not in global order. Only values handed out from one block are in increasing
// order. Some values are never handed out. These include the unused rest of a block and
// math.MaxUint64, which is reserved so the end of the range can be stored.
//
// The read-only header and shared counter are kept on separate cache lines. A Sequence
// must be created with NewSequence and must not be copied after first use.
type Sequence struct {
	// slots holds one padded slot per stripe.
	slots []sequenceSlot

	// start is the first value of the range.
	start uint64

	// capacity is the number of values in the range, from start up to but not including
	// math.MaxUint64.
	capacity uint64

	// blockMask is the block size minus one. The size is a power of two.
	blockMask uint64

	// slotMask is the slot count minus one. The count is a power of two.
	slotMask int

	_ [sequenceHeaderPadding]byte

	// allocated counts the values of the range given to blocks so far, from zero to
	// capacity. It is written once per block.
	allocated atomic.Uint64

	_ [cacheLineSize - 8]byte
}

// SequenceOption changes how NewSequence builds a Sequence.
type SequenceOption func(*sequenceConfig)

// sequenceConfig holds the settings gathered from SequenceOption values.
type sequenceConfig struct {
	// blockSize is the number of values a slot takes at a time.
	blockSize uint64

	// start is the first value of the range.
	start uint64
}

// sequenceSlot is one cache-line padded slot of a Sequence.
type sequenceSlot struct {
	// next is the offset of the next value in the current block.
	next atomic.Uint64

	_ [cacheLineSize - 8]byte
}

// slotOutcome reports what one attempt to take from a slot did.
type slotOutcome uint8

// NewSequence returns a Sequence sized for this process.
//
// Takes options (...SequenceOption) which change the defaults. Nil options are ignored.
//
// Returns *Sequence which is ready to use.
// Returns error which wraps ErrInvalidSequenceOption when an option is out of range.
func NewSequence(options ...SequenceOption) (*Sequence, error) {
	config := sequenceConfig{
		blockSize: defaultSequenceBlockSize,
		start:     0,
	}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return newSequenceWithSlots(config, stripeCount()), nil
}

// Next returns a value that no other call on this Sequence has returned or will return.
//
// Returns uint64 which is unique within this Sequence and below math.MaxUint64.
// Returns error which is ErrSequenceExhausted when no value is left in the range or in
// this processor's block. Later calls on other processors may still return values left in
// their blocks, but never a value returned before.
func (s *Sequence) Next() (uint64, error) {
	offset, ok := s.nextFromCurrentSlot()
	if !ok {
		return 0, ErrSequenceExhausted
	}
	return s.start + offset, nil
}

// nextByToken takes the next offset from the slot last used on the current P (logical
// processor), found through a sync.Pool token.
//
// Returns uint64 which is the offset of the value from the start.
// Returns bool which is false when the range is used up.
func (s *Sequence) nextByToken() (uint64, bool) {
	token := acquireToken()
	for {
		offset, outcome := s.takeFromSlot(&s.slots[int(token.index)&s.slotMask])
		if outcome != slotContended {
			releaseToken(token)
			return offset, outcome == slotTaken
		}
		token.rehash()
	}
}

// takeFromSlot makes one attempt to take the next offset from slot.
//
// Takes slot (*sequenceSlot) which is one of s.slots.
//
// Returns uint64 which is the offset, valid only with slotTaken.
// Returns slotOutcome which says whether an offset was taken.
func (s *Sequence) takeFromSlot(slot *sequenceSlot) (uint64, slotOutcome) {
	current := slot.next.Load()
	if current&s.blockMask != 0 {
		if slot.next.CompareAndSwap(current, current+1) {
			return current, slotTaken
		}
		return 0, slotContended
	}
	blockStart, whole, ok := s.reserve()
	if !ok {
		return 0, slotExhausted
	}
	if whole && s.blockMask != 0 {
		slot.next.CompareAndSwap(current, blockStart+1)
	}
	return blockStart, slotTaken
}

// reserve takes a block from the shared range.
//
// Returns uint64 which is the first offset taken.
// Returns bool which is true when a whole block was taken and false when only one offset
// was taken.
// Returns bool which is false when the range is used up.
func (s *Sequence) reserve() (blockStart uint64, whole, ok bool) {
	blockSize := s.blockMask + 1
	for {
		allocated := s.allocated.Load()
		remaining := s.capacity - allocated
		if remaining == 0 {
			return 0, false, false
		}
		take := uint64(1)
		if remaining >= blockSize {
			take = blockSize
		}
		if s.allocated.CompareAndSwap(allocated, allocated+take) {
			return allocated, take == blockSize, true
		}
	}
}

// validate checks that the settings describe a usable Sequence.
//
// Returns error which wraps ErrInvalidSequenceOption when a setting is out of range.
func (c *sequenceConfig) validate() error {
	if c.blockSize == 0 || c.blockSize > maxSequenceBlockSize || c.blockSize&(c.blockSize-1) != 0 {
		return fmt.Errorf("%w: block size %d must be a power of two from 1 to %d",
			ErrInvalidSequenceOption, c.blockSize, maxSequenceBlockSize)
	}
	if c.start == math.MaxUint64 {
		return fmt.Errorf("%w: start must be below %d", ErrInvalidSequenceOption, uint64(math.MaxUint64))
	}
	return nil
}

// WithBlockSize sets how many values a slot takes from the shared range at a time.
//
// Takes size (uint64) which must be a power of two from 1 to 1048576.
//
// Returns SequenceOption which applies the setting.
func WithBlockSize(size uint64) SequenceOption {
	return func(config *sequenceConfig) {
		config.blockSize = size
	}
}

// WithStart sets the first value of the range.
//
// Takes start (uint64) which must be below math.MaxUint64.
//
// Returns SequenceOption which applies the setting.
func WithStart(start uint64) SequenceOption {
	return func(config *sequenceConfig) {
		config.start = start
	}
}

// newSequenceWithSlots returns a Sequence with slotTotal slots.
//
// Takes config (sequenceConfig) which must have passed validate.
// Takes slotTotal (int) which must be a power of two.
//
// Returns *Sequence which has handed out nothing yet.
func newSequenceWithSlots(config sequenceConfig, slotTotal int) *Sequence {
	return &Sequence{
		slots:     make([]sequenceSlot, slotTotal),
		start:     config.start,
		capacity:  math.MaxUint64 - config.start,
		blockMask: config.blockSize - 1,
		slotMask:  slotTotal - 1,
	}
}
