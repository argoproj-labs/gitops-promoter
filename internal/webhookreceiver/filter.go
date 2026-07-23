package webhookreceiver

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/expr-lang/expr"
)

// evaluateWebhookFilter runs a boolean expr with Payload bound to the decoded webhook JSON body.
func evaluateWebhookFilter(expression string, payloadJSON []byte) (bool, error) {
	var payload map[string]any
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return false, fmt.Errorf("unmarshal webhook payload: %w", err)
	}
	program, err := expr.Compile(expression, expr.AsBool(), expr.Env(map[string]any{
		"Payload": map[string]any{},
	}))
	if err != nil {
		return false, fmt.Errorf("compile webhook filter expression: %w", err)
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
