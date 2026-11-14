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
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	benchEntryCount = 10_000
	benchSequenceLength = 1 << 16
	benchSequenceMask = benchSequenceLength - 1
	benchZipfExponent = 1.1
	benchWorkerStride = 7919
	sampleRate = 64
	benchMetricMemory = "heap-B/10k-entries"
	benchMetricOverhead = "counter-B/10k-entries"
)

type benchPayload [2 * cacheLineSize]byte

type noopEntry struct {
	payload benchPayload
}

type atomicEntry struct {
	payload benchPayload
	hits    atomic.Int64
}

type counterEntry struct {
	hits    Counter
	payload benchPayload
}

type adderEntry struct {
	hits    *Adder
	payload benchPayload
}

type sampledEntry struct {
	payload benchPayload
	hits    sampledCounter
}

type sampledCounter struct {
	value atomic.Int64
}

func (s *sampledCounter) add() {
	if rand.Uint32()&(sampleRate-1) == 0 {
		s.value.Add(sampleRate)
	}
}

func (s *sampledCounter) estimate() int64 {
	return s.value.Load()
}

type benchSelector struct {
	newAdder     func() *Adder
	add          func(*Adder, int64)
	selectStripe func() uint32
	name         string
}

type accessPattern struct {
	name    string
	indices []uint32
}

type candidateSetup[E any] func(b *testing.B) (newEntry func() *E, hit func(*E), finish func(entries []*E))

var (
	benchPatterns = sync.OnceValue(buildPatterns)
	noopHeapBytes = sync.OnceValue(func() uint64 {
		before := liveHeapBytes()
		entries := make([]*noopEntry, benchEntryCount)
		for index := range entries {
			entries[index] = new(noopEntry)
		}
		after := liveHeapBytes()
		runtime.KeepAlive(entries)
		return after - before
	})
)

func buildPatterns() []accessPattern {
	source := rand.New(rand.NewPCG(1, 2))
	zipf := rand.NewZipf(source, benchZipfExponent, 1, benchEntryCount-1)
	hot := make([]uint32, benchSequenceLength)
	zipfIndices := make([]uint32, benchSequenceLength)
	uniform := make([]uint32, benchSequenceLength)
	for index := range benchSequenceLength {
		zipfIndices[index] = uint32(zipf.Uint64())
		uniform[index] = source.Uint32N(benchEntryCount)
	}
	return []accessPattern{
		{name: "hot", indices: hot},
		{name: "zipf", indices: zipfIndices},
		{name: "uniform", indices: uniform},
	}
}

func liveHeapBytes() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func BenchmarkHitCount(b *testing.B) {
	for _, pattern := range benchPatterns() {
		b.Run("pattern="+pattern.name, func(b *testing.B) {
			b.Run("candidate=noop", func(b *testing.B) { runCandidate(b, pattern, setupNoop) })
			b.Run("candidate=atomic", func(b *testing.B) { runCandidate(b, pattern, setupAtomic) })
			b.Run("candidate=counter", func(b *testing.B) { runCandidate(b, pattern, setupCounter) })
			b.Run("candidate=adder", func(b *testing.B) { runCandidate(b, pattern, setupAdder) })
			b.Run("candidate=sampled", func(b *testing.B) { runCandidate(b, pattern, setupSampled) })
		})
	}
}

func runCandidate[E any](b *testing.B, pattern accessPattern, setup candidateSetup[E]) {
	b.Helper()
	baseline := noopHeapBytes()
	before := liveHeapBytes()
	newEntry, hit, finish := setup(b)
	entries := make([]*E, benchEntryCount)
	for index := range entries {
		entries[index] = newEntry()
	}
	var workers atomic.Uint32

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		position := workers.Add(1) * benchWorkerStride
		for pb.Next() {
			hit(entries[pattern.indices[position&benchSequenceMask]])
			position++
		}
	})
	b.StopTimer()

	finish(entries)
	held := liveHeapBytes() - before
	b.ReportMetric(float64(held), benchMetricMemory)
	b.ReportMetric(float64(int64(held)-int64(baseline)), benchMetricOverhead)
	runtime.KeepAlive(entries)
}

func setupNoop(*testing.B) (func() *noopEntry, func(*noopEntry), func([]*noopEntry)) {
	return func() *noopEntry { return new(noopEntry) },
		func(*noopEntry) {},
		func([]*noopEntry) {}
}

func setupAtomic(b *testing.B) (func() *atomicEntry, func(*atomicEntry), func([]*atomicEntry)) {
	return func() *atomicEntry { return new(atomicEntry) },
		func(entry *atomicEntry) { entry.hits.Add(1) },
		func(entries []*atomicEntry) {
			checkTotal(b, sumEntries(entries, func(entry *atomicEntry) int64 { return entry.hits.Load() }))
		}
}

func setupCounter(b *testing.B) (func() *counterEntry, func(*counterEntry), func([]*counterEntry)) {
	return func() *counterEntry { return new(counterEntry) },
		func(entry *counterEntry) { entry.hits.Add(1) },
		func(entries []*counterEntry) {
			checkTotal(b, sumEntries(entries, func(entry *counterEntry) int64 { return entry.hits.Value() }))
			inflated := sumEntries(entries, func(entry *counterEntry) int64 {
				if entry.hits.inflated() {
					return 1
				}
				return 0
			})
			b.ReportMetric(float64(inflated), "inflated/10k-entries")
		}
}

func setupAdder(b *testing.B) (func() *adderEntry, func(*adderEntry), func([]*adderEntry)) {
	return func() *adderEntry { return &adderEntry{hits: NewAdder()} },
		func(entry *adderEntry) { entry.hits.Add(1) },
		func(entries []*adderEntry) {
			checkTotal(b, sumEntries(entries, func(entry *adderEntry) int64 { return entry.hits.Value() }))
		}
}

func setupSampled(b *testing.B) (func() *sampledEntry, func(*sampledEntry), func([]*sampledEntry)) {
	return func() *sampledEntry { return new(sampledEntry) },
		func(entry *sampledEntry) { entry.hits.add() },
		func(entries []*sampledEntry) {
			estimate := sumEntries(entries, func(entry *sampledEntry) int64 { return entry.hits.estimate() })
			b.ReportMetric(100*math.Abs(float64(estimate-int64(b.N)))/float64(b.N), "error-%")
		}
}

func sumEntries[E any](entries []*E, value func(*E) int64) int64 {
	var total int64
	for _, entry := range entries {
		total += value(entry)
	}
	return total
}

func checkTotal(b *testing.B, total int64) {
	b.Helper()
	if total != int64(b.N) {
		b.Fatalf("counted %d hits, expected %d", total, b.N)
	}
}

func BenchmarkUncontendedAdd(b *testing.B) {
	b.Run("candidate=atomic", func(b *testing.B) {
		var value atomic.Int64
		for b.Loop() {
			value.Add(1)
		}
	})
	b.Run("candidate=counter", func(b *testing.B) {
		var counter Counter
		for b.Loop() {
			counter.Add(1)
		}
	})
	b.Run("candidate=adder", func(b *testing.B) {
		adder := NewAdder()
		for b.Loop() {
			adder.Add(1)
		}
	})
	b.Run("candidate=sampled", func(b *testing.B) {
		var counter sampledCounter
		for b.Loop() {
			counter.add()
		}
	})
}

func BenchmarkValue(b *testing.B) {
	b.Run("candidate=counter", func(b *testing.B) {
		var counter Counter
		counter.Add(1)
		for b.Loop() {
			_ = counter.Value()
		}
	})
	b.Run("candidate=counter-inflated", func(b *testing.B) {
		var counter Counter
		counter.inflate().Add(1)
		for b.Loop() {
			_ = counter.Value()
		}
	})
	b.Run("candidate=adder", func(b *testing.B) {
		adder := NewAdder()
		adder.Add(1)
		for b.Loop() {
			_ = adder.Value()
		}
	})
}

func TestSampledCounterEstimate(t *testing.T) {
	t.Parallel()

	const adds = 1_000_000
	var counter sampledCounter
	for range adds {
		counter.add()
	}
	deviation := math.Sqrt(adds * (sampleRate - 1))
	assert.InDelta(t, adds, counter.estimate(), 6*deviation)
}

func TestBenchPatterns(t *testing.T) {
	t.Parallel()

	patterns := benchPatterns()
	assert.Len(t, patterns, 3)
	for _, pattern := range patterns {
		assert.Len(t, pattern.indices, benchSequenceLength, pattern.name)
		for _, index := range pattern.indices {
			if index >= benchEntryCount {
				t.Fatalf("pattern %s has index %d outside the entries", pattern.name, index)
			}
		}
	}
}

func benchSelectors() []benchSelector {
	selectors := []benchSelector{{
		name:     "pool-token",
		newAdder: NewAdder,
		add:      (*Adder).addByToken,
		selectStripe: func() uint32 {
			token := acquireToken()
			index := token.index
			releaseToken(token)
			return index
		},
	}}
	return append(selectors, pinBenchSelectors()...)
}

func BenchmarkStripeSelector(b *testing.B) {
	for _, candidate := range benchSelectors() {
		b.Run("selector="+candidate.name, func(b *testing.B) {
			var sink uint32
			for b.Loop() {
				sink += candidate.selectStripe()
			}
			runtime.KeepAlive(sink)
		})
	}
}

func BenchmarkAdderHotKey(b *testing.B) {
	for _, candidate := range benchSelectors() {
		b.Run("selector="+candidate.name, func(b *testing.B) {
			adder := candidate.newAdder()
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					candidate.add(adder, 1)
				}
			})
			b.StopTimer()
			checkTotal(b, adder.Value())
		})
	}
}

func TestBenchSelectors(t *testing.T) {
	t.Parallel()

	for _, candidate := range benchSelectors() {
		t.Run(candidate.name, func(t *testing.T) {
			t.Parallel()
			adder := candidate.newAdder()
			candidate.add(adder, 3)
			candidate.add(adder, -1)
			_ = candidate.selectStripe()
			assert.Equal(t, int64(2), adder.Value())
		})
	}
}

func sequenceCandidates(b *testing.B) []struct {
	next func() uint64
	name string
} {
	b.Helper()
	var shared atomic.Uint64
	sequence, err := NewSequence()
	if err != nil {
		b.Fatal(err)
	}
	tokenSequence, err := NewSequence()
	if err != nil {
		b.Fatal(err)
	}
	return []struct {
		next func() uint64
		name string
	}{
		{name: "atomic", next: func() uint64 { return shared.Add(1) }},
		{name: "sequence", next: func() uint64 {
			value, _ := sequence.Next()
			return value
		}},
		{name: "sequence-token", next: func() uint64 {
			offset, _ := tokenSequence.nextByToken()
			return offset
		}},
	}
}

func BenchmarkSequenceNext(b *testing.B) {
	for _, candidate := range sequenceCandidates(b) {
		b.Run("candidate="+candidate.name, func(b *testing.B) {
			var sink uint64
			for b.Loop() {
				sink += candidate.next()
			}
			runtime.KeepAlive(sink)
		})
	}
}

func BenchmarkSequenceNextParallel(b *testing.B) {
	for _, candidate := range sequenceCandidates(b) {
		b.Run("candidate="+candidate.name, func(b *testing.B) {
			b.ReportAllocs()
			b.RunParallel(func(pb *testing.PB) {
				var sink uint64
				for pb.Next() {
					sink += candidate.next()
				}
				runtime.KeepAlive(sink)
			})
		})
	}
}
