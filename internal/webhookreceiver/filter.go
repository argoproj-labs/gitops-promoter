package webhookreceiver

import (
	"fmt"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/golang/groupcache/lru"
)

// maxWebhookFilterCacheEntries bounds the compiled-filter LRU so unique expressions from
// churned WRCS resources cannot grow controller memory without bound.
const maxWebhookFilterCacheEntries = 2048

// webhookFilterProgramCache is an LRU of compiled webhook filter programs keyed by expression.
// The upstream lru.Cache is not safe for concurrent use; mu serializes get/put.
var webhookFilterProgramCache = struct {
	mu  sync.Mutex
	lru *lru.Cache
}{
	lru: lru.New(maxWebhookFilterCacheEntries),
}

func getCompiledWebhookFilter(expression string) (*vm.Program, error) {
	webhookFilterProgramCache.mu.Lock()
	defer webhookFilterProgramCache.mu.Unlock()

	if cached, ok := webhookFilterProgramCache.lru.Get(expression); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, fmt.Errorf("cached webhook filter value is not a *vm.Program")
		}
		return program, nil
	}

	program, err := expr.Compile(expression, expr.AsBool(), expr.Env(map[string]any{
		"Payload": map[string]any{},
	}))
	if err != nil {
		return nil, fmt.Errorf("compile webhook filter expression: %w", err)
	}
	webhookFilterProgramCache.lru.Add(expression, program)
	return program, nil
}

// evaluateWebhookFilter runs a boolean expr with Payload bound to the decoded webhook JSON object.
func evaluateWebhookFilter(expression string, payload map[string]any) (bool, error) {
	program, err := getCompiledWebhookFilter(expression)
	if err != nil {
		return false, err
	}
	out, err := expr.Run(program, map[string]any{"Payload": payload})
	if err != nil {
		return false, fmt.Errorf("run webhook filter expression: %w", err)
	}
	matched, ok := out.(bool)
	if !ok {
		return false, fmt.Errorf("webhook filter expression did not return bool")
	}
	return matched, nil
}
