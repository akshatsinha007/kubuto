// Copyright 2024 The Kubuto Authors
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

package scanner

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestParallelMap_PreservesOrder(t *testing.T) {
	items := []int{5, 4, 3, 2, 1, 0}
	results := parallelMap(items, 3, func(i int) int {
		// Sleep inversely to value so completion order is scrambled
		// relative to input order — this only passes if parallelMap
		// places each result at its input index, not completion order.
		time.Sleep(time.Duration(i) * time.Millisecond)
		return i * 10
	})
	assert.Equal(t, []int{50, 40, 30, 20, 10, 0}, results)
}

func TestParallelMap_RespectsLimit(t *testing.T) {
	const limit = 3
	var current, max int32

	items := make([]int, 20)
	parallelMap(items, limit, func(int) int {
		n := atomic.AddInt32(&current, 1)
		for {
			m := atomic.LoadInt32(&max)
			if n <= m || atomic.CompareAndSwapInt32(&max, m, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		return 0
	})

	assert.LessOrEqual(t, int(max), limit, "never more than %d concurrent calls", limit)
	assert.Equal(t, int32(limit), max, "should actually reach the limit, not run under-concurrently")
}

func TestParallelMap_EmptyInput(t *testing.T) {
	results := parallelMap([]int{}, 5, func(i int) int { return i })
	assert.Empty(t, results)
}

func TestParallelMap_LimitLessThanOne_RunsSequentiallyNotUnbounded(t *testing.T) {
	// limit=0 must behave as limit=1, not "unbounded" — a caller passing
	// a zero-value by mistake should get safe (if slow) behavior, never
	// an accidental fan-out.
	var current, max int32
	items := make([]int, 10)
	parallelMap(items, 0, func(int) int {
		n := atomic.AddInt32(&current, 1)
		if n > atomic.LoadInt32(&max) {
			atomic.StoreInt32(&max, n)
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&current, -1)
		return 0
	})
	assert.Equal(t, int32(1), max)
}
