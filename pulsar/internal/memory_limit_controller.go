// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package internal

import (
	"context"
	"sync"
	"sync/atomic"
)

type MemoryLimitController interface {
	ReserveMemory(ctx context.Context, size int64) bool
	TryReserveMemory(size int64) bool
	ForceReserveMemory(size int64)
	ReleaseMemory(size int64)
	CurrentUsage() int64
	CurrentUsagePercent() float64
	IsMemoryLimited() bool
}

type memoryLimitController struct {
	limit        int64
	chCond       *chCond
	currentUsage int64
}

func NewMemoryLimitController(limit int64) MemoryLimitController {
	mlc := &memoryLimitController{
		limit:  limit,
		chCond: newCond(&sync.Mutex{}),
	}
	return mlc
}

func (m *memoryLimitController) ReserveMemory(ctx context.Context, size int64) bool {
	if !m.TryReserveMemory(size) {
		m.chCond.L.Lock()
		defer m.chCond.L.Unlock()

		for !m.TryReserveMemory(size) {
			if !m.chCond.waitWithContext(ctx) {
				return false
			}
		}
	}
	return true
}

func (m *memoryLimitController) TryReserveMemory(size int64) bool {
	for {
		current := atomic.LoadInt64(&m.currentUsage)
		newUsage := current + size

		// This condition means we allowed one request to go over the limit.
		if m.IsMemoryLimited() && current > m.limit {
			return false
		}

		if atomic.CompareAndSwapInt64(&m.currentUsage, current, newUsage) {
			return true
		}
	}
}

func (m *memoryLimitController) ForceReserveMemory(size int64) {
	atomic.AddInt64(&m.currentUsage, size)
}

func (m *memoryLimitController) ReleaseMemory(size int64) {
	newUsage := atomic.AddInt64(&m.currentUsage, -size)
	if newUsage+size > m.limit && newUsage <= m.limit {
		m.chCond.broadcast()
	}
}

func (m *memoryLimitController) CurrentUsage() int64 {
	return atomic.LoadInt64(&m.currentUsage)
}

func (m *memoryLimitController) CurrentUsagePercent() float64 {
	return float64(atomic.LoadInt64(&m.currentUsage)) / float64(m.limit)
}

func (m *memoryLimitController) IsMemoryLimited() bool {
	return m.limit > 0
}
