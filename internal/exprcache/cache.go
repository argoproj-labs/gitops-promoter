/*
Copyright 2026.

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

// Package exprcache provides a bounded LRU cache of compiled expr programs shared by the controllers
// that evaluate user-supplied expr expressions (WebRequestCommitStatus, KubernetesResourceCommitStatus).
package exprcache

import (
	"fmt"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/golang/groupcache/lru"
)

// DefaultMaxEntries bounds the compile cache so unique expressions from many resources (for example churned
// or auto-generated gates) cannot grow memory without bound. Least-recently-used programs are evicted when
// over this limit.
const DefaultMaxEntries = 4096

// Key identifies a compiled expression in the cache. Prefix distinguishes entries so the same source
// expression compiled with different options (for example trigger vs validation) stays separate.
type Key struct {
	Prefix     string
	Expression string
}

// Cache is an LRU of compiled programs (github.com/golang/groupcache/lru) keyed by Key.
// The upstream lru.Cache is not safe for concurrent use; a mutex serializes every access, including Get,
// because lru.Get mutates recency state.
type Cache struct {
	lru *lru.Cache
	mu  sync.Mutex
}

// New returns a Cache holding at most maxEntries programs. Values below 1 use DefaultMaxEntries.
func New(maxEntries int) *Cache {
	if maxEntries < 1 {
		maxEntries = DefaultMaxEntries
	}
	return &Cache{lru: lru.New(maxEntries)}
}

// Get returns the cached program for key, if any.
func (c *Cache) Get(key Key) (*vm.Program, bool) {
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

// Put stores program under key, evicting the least recently used entry when over capacity.
func (c *Cache) Put(key Key, program *vm.Program) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lru.Add(key, program)
}

// GetOrCompile returns the cached program for key or compiles key.Expression with opts and caches it.
func (c *Cache) GetOrCompile(key Key, opts ...expr.Option) (*vm.Program, error) {
	if program, ok := c.Get(key); ok {
		return program, nil
	}

	program, err := expr.Compile(key.Expression, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	c.Put(key, program)
	return program, nil
}
