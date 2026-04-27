/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webrequest

import (
	"container/list"
	"sync"

	"github.com/expr-lang/expr/vm"
)

// maxCompiledExpressionCacheEntries bounds the compile cache so unique expressions from many resources
// (e.g. churned or auto-generated WRCS) cannot grow memory without bound. Least-recently-used programs
// are evicted when over this limit.
const maxCompiledExpressionCacheEntries = 4096

// compileCache is an LRU of compiled programs keyed by (prefix, expression). Evicts the least recently
// used entry when the number of entries exceeds max.
type compileCache struct {
	mu  sync.Mutex
	max int
	// lru is front = most recently used, back = least recently used. Each element's Value is *cacheEntry.
	lru   *list.List
	byKey map[expressionCacheKey]*list.Element
}

type cacheEntry struct {
	key     expressionCacheKey
	program *vm.Program
}

func newCompileCache(max int) *compileCache {
	if max < 1 {
		max = maxCompiledExpressionCacheEntries
	}
	return &compileCache{
		max:   max,
		lru:   list.New(),
		byKey: make(map[expressionCacheKey]*list.Element),
	}
}

func (c *compileCache) get(key expressionCacheKey) (*vm.Program, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(el)
	return el.Value.(*cacheEntry).program, true
}

func (c *compileCache) put(key expressionCacheKey, program *vm.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		el.Value.(*cacheEntry).program = program
		c.lru.MoveToFront(el)
		return
	}
	ent := &cacheEntry{key: key, program: program}
	elem := c.lru.PushFront(ent)
	c.byKey[key] = elem
	for c.lru.Len() > c.max {
		back := c.lru.Back()
		old := back.Value.(*cacheEntry)
		delete(c.byKey, old.key)
		c.lru.Remove(back)
	}
}

// expressionCacheKey identifies a compiled expression in the cache. Prefix distinguishes entries so the same
// source expression compiled with different options (e.g. trigger vs validation) stays separate.
type expressionCacheKey struct {
	Prefix     string
	Expression string
}

// newExpressionEvaluatorWithCompileCacheMax returns an evaluator whose compile LRU capacity is max (tests).
func newExpressionEvaluatorWithCompileCacheMax(max int) *ExpressionEvaluator {
	return &ExpressionEvaluator{cache: newCompileCache(max)}
}
