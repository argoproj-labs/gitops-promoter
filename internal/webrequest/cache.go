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
	"sync"

	"github.com/expr-lang/expr/vm"
	"github.com/golang/groupcache/lru"
)

// maxCompiledExpressionCacheEntries bounds the compile cache so unique expressions from many resources
// (e.g. churned or auto-generated WRCS) cannot grow memory without bound. Least-recently-used programs
// are evicted when over this limit.
const maxCompiledExpressionCacheEntries = 4096

// compileCache is an LRU of compiled programs (github.com/golang/groupcache/lru) keyed by
// (prefix, expression). The upstream lru.Cache is not safe for concurrent use; a mutex serializes get/put.
type compileCache struct {
	lru *lru.Cache
	mu  sync.Mutex
}

func newCompileCache(maxEntries int) *compileCache {
	if maxEntries < 1 {
		maxEntries = maxCompiledExpressionCacheEntries
	}
	return &compileCache{
		lru: lru.New(maxEntries),
	}
}

func (c *compileCache) get(key expressionCacheKey) (*vm.Program, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.lru.Get(key)
	if !ok {
		return nil, false
	}
	program, ok := v.(*vm.Program)
	if !ok {
		return nil, false
	}
	return program, true
}

func (c *compileCache) put(key expressionCacheKey, program *vm.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, program)
}

// expressionCacheKey identifies a compiled expression in the cache. Prefix distinguishes entries so the same
// source expression compiled with different options (e.g. trigger vs validation) stays separate.
type expressionCacheKey struct {
	Prefix     string
	Expression string
}

// newExpressionEvaluatorWithCompileCacheMax returns an evaluator whose compile LRU capacity is maxEntries (tests).
func newExpressionEvaluatorWithCompileCacheMax(maxEntries int) *ExpressionEvaluator {
	return &ExpressionEvaluator{cache: newCompileCache(maxEntries)}
}
