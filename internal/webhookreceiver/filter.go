package webhookreceiver

import (
	"errors"
	"fmt"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// webhookFilterProgramCache caches compiled filter expressions keyed by expression string.
// Expressions are stable per WRCS spec, so compiling once avoids a hot path under frequent webhooks.
var webhookFilterProgramCache sync.Map

func getCompiledWebhookFilter(expression string) (*vm.Program, error) {
	if cached, ok := webhookFilterProgramCache.Load(expression); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, errors.New("cached webhook filter value is not a *vm.Program")
		}
		return program, nil
	}

	program, err := expr.Compile(expression, expr.AsBool(), expr.Env(map[string]any{
		"Payload": map[string]any{},
	}))
	if err != nil {
		return nil, fmt.Errorf("compile webhook filter expression: %w", err)
	}
	webhookFilterProgramCache.Store(expression, program)
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
		return false, errors.New("webhook filter expression did not return bool")
	}
	return matched, nil
}
