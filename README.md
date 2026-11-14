# sharded

`sharded` provides counters and id sequences for Go that stay fast when many goroutines
update them at once.

An `atomic.Int64` that every goroutine writes moves its cache line between cores on each
write, so throughput falls as you add cores. `sharded` gives each Go processor (P) its own
shard, each on its own cache line, and sums them when you read. Adds on different Ps then
rarely touch the same line. In exchange a read costs one load per shard and may miss
adds that overlap the read.

## Install

```sh
go get piko.sh/sharded
```

## Usage

```go
package main

import (
	"fmt"
	"sync"

	"piko.sh/sharded"
)

type entry struct {
	hits sharded.Counter
	data []byte
}

func main() {
	misses := sharded.NewAdder()
	hot := &entry{}

	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			for range 1000 {
				hot.hits.Add(1)
				misses.Add(1)
			}
		})
	}
	group.Wait()

	fmt.Println(hot.hits.Value(), misses.Value())
}
```

The example prints `8000 8000`.

## What it provides

- **`Adder`** is a striped `int64` total, built with **`NewAdder`**. It has one 64-byte
  stripe per P (`GOMAXPROCS`, rounded up to a power of two and capped at 4096) and a header
  padded to one cache line. Use it for a small number of shared totals, such as cache hits
  and misses.
- **`Counter`** is an exact per-item `int64` counter whose zero value is ready to use. It
  starts as a single word, updated with compare and swap, and moves to an `Adder` the first
  time one `Add` fails its compare and swap four times in a row. Cold counters stay at
  16 bytes. Only hot counters pay for stripes. Use it for counts kept on every item, such
  as hits on each cache entry, when only some items are hot.
- `Adder` and `Counter` both have **`Add(delta int64)`** and **`Value() int64`**, are safe
  for any number of goroutines, and wrap on overflow like `atomic.Int64`.
- **`Sequence`** hands out unique `uint64` values, such as ids, built with
  **`NewSequence`**. Each P takes a block of values (256 by default, set with
  **`WithBlockSize`**) from one shared counter and hands them out from its own slot, so the
  shared counter is written once per block instead of once per value. **`WithStart`** sets
  the first value. **`Next() (uint64, error)`** returns the next value, or
  `ErrSequenceExhausted` once the range is used up.

No type starts a goroutine. Values must not be copied after first use.

## Semantics

- **Adds are never lost.** Every `Add` is applied exactly once with an atomic operation.
- **`Value` is exact after concurrent updates finish.** Once the writers stop, `Value` returns
  the true total.
- **`Value` may miss adds that overlap the read.** It loads the shards one after another,
  so an add that runs during the read may or may not be counted. A later `Value` sees it.

If you need compare and swap, or an exact snapshot while writers run, use `atomic.Int64`.

`Sequence` has these properties.

- **Every value is unique.** No value is ever returned twice by one `Sequence`, by any
  goroutine, for as long as it lives. This holds in both builds and after `GOMAXPROCS`
  changes.
- **Values are not in global order.** Only values from one block come out in increasing
  order. Two goroutines, or one goroutine that moves to another P, can see a later call
  return a smaller value.
- **There are gaps.** The unused rest of a block is never handed out by anyone else, and
  `math.MaxUint64` is kept back so the end of the range can be stored. Do not use the
  values to count how many were handed out.
- **The range never wraps.** Blocks are taken from the shared counter with compare and
  swap, so it stops at the end of the range instead of wrapping to values already handed
  out. Near the end, values are taken one at a time, so every value up to
  `math.MaxUint64 - 1` can be used. After that, `Next` returns `ErrSequenceExhausted` and
  never a reused value. A later call on another P may still return a value left in its
  block. With a start of zero, a million ids per second would take more than half a
  million years to reach the end.
- **`NewSequence` checks its options.** The block size must be a power of two from 1 to
  1048576 and the start must be below `math.MaxUint64`. Otherwise it returns an error
  that wraps `ErrInvalidSequenceOption`.

Use `Sequence` when many goroutines need unique ids at a high rate, such as request,
trace or object ids that only have to be distinct. Do not use it when ids must sort by
the time they were made, must be dense with no gaps, or must be unique across processes
or restarts. A `Sequence` only knows about itself. If ids are rare, one goroutine makes
most of them, or the order matters, an `atomic.Uint64` is simpler and faster.

## Benchmarks

These results were measured in Piko on an Intel Core Ultra 7 155U, pinned to the eight
E-cores with `taskset -c 4-11`.

| Workload | `atomic.Int64` | `Counter` |
| --- | --- | --- |
| One hot key, 8 CPUs | 25.4 ns/op | 1.84 ns/op |
| One goroutine, no contention | baseline | about 23% slower |
| Memory per cold counter | 8 B | 16 B |

Under contention a shared atomic word costs more as cores are added, while `Counter` costs
less, because each P writes its own shard. On a single thread `Counter` pays for a
compare and swap loop instead of one atomic add. Run the benchmarks with the following
command.

```sh
go test -run '^$' -bench . -cpu 1,4,8 ./...
```

The `Sequence` results compare it with one shared `atomic.Uint64.Add` on the same machine
and cores. Each result uses eight runs of `BenchmarkSequenceNext` or
`BenchmarkSequenceNextParallel`. Each call goes through a function value, which adds the
same small cost to every column.

| Workload | `atomic.Uint64` | `Sequence` | `Sequence`, safe build path |
| --- | --- | --- | --- |
| One goroutine | 6.47 ns/op | 9.68 ns/op | 14.36 ns/op |
| Every P at once, 1 CPU | 6.39 ns/op | 9.89 ns/op | 16.81 ns/op |
| Every P at once, 4 CPUs | 27.0 ns/op | 2.53 ns/op | 4.71 ns/op |
| Every P at once, 8 CPUs | 26.1 ns/op | 2.04 ns/op | 3.48 ns/op |

Without contention a `Sequence` costs about 50% more than an atomic add, because it pins
the goroutine and uses a load and a compare and swap. With four or more CPUs taking ids at
once it is about ten times faster, because each P writes its own slot and touches the
shared counter only once per block.