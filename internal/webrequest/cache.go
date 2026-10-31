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
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
)

// maxCompiledExpressionCacheEntries bounds the compile cache so unique expressions from many resources
// (e.g. churned or auto-generated WRCS) cannot grow memory without bound. Least-recently-used programs
// are evicted when over this limit.
const maxCompiledExpressionCacheEntries = exprcache.DefaultMaxEntries

// expressionCacheKey identifies a compiled expression in the cache. Prefix distinguishes entries so the same
// source expression compiled with different options (e.g. trigger vs validation) stays separate.
type expressionCacheKey = exprcache.Key

// newExpressionEvaluatorWithCompileCacheMax returns an evaluator whose compile LRU capacity is maxEntries (tests).
func newExpressionEvaluatorWithCompileCacheMax(maxEntries int) *ExpressionEvaluator {
	return &ExpressionEvaluator{cache: exprcache.New(maxEntries)}
}
