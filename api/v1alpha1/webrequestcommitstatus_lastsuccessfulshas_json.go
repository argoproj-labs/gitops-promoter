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

package v1alpha1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// UnmarshalJSON for promotionStrategyContext decodes lastSuccessfulShas from either the normal array
// or a legacy JSON object (branch -> SHA) so list/watch works against mis-persisted status.
func (s *WebRequestCommitStatusPromotionStrategyContextStatus) UnmarshalJSON(data []byte) error {
	type psc WebRequestCommitStatusPromotionStrategyContextStatus
	aux := &struct {
		LastSuccessfulShas json.RawMessage `json:"lastSuccessfulShas,omitempty"`
		*psc
	}{
		psc: (*psc)(s),
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	if len(bytes.TrimSpace(aux.LastSuccessfulShas)) == 0 {
		return nil
	}
	var shas webRequestCommitStatusLastSuccessfulShasWire
	if err := shas.UnmarshalJSON(aux.LastSuccessfulShas); err != nil {
		return fmt.Errorf("promotionStrategyContext.lastSuccessfulShas: %w", err)
	}
	s.LastSuccessfulShas = []WebRequestCommitStatusLastSuccessfulShaItem(shas)
	return nil
}

// webRequestCommitStatusLastSuccessfulShasWire is only used for JSON decoding (not an API field) so
// controller-gen listType markers stay on the real [] slice field.
type webRequestCommitStatusLastSuccessfulShasWire []WebRequestCommitStatusLastSuccessfulShaItem

func (s *webRequestCommitStatusLastSuccessfulShasWire) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		*s = nil
		return nil
	}
	switch data[0] {
	case '[':
		var arr []WebRequestCommitStatusLastSuccessfulShaItem
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*s = arr
		return nil
	case '{':
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		items := make([]WebRequestCommitStatusLastSuccessfulShaItem, 0, len(m))
		for branch, v := range m {
			sha := ""
			if v != nil {
				switch t := v.(type) {
				case string:
					sha = t
				default:
					sha = fmt.Sprint(t)
				}
			}
			items = append(items, WebRequestCommitStatusLastSuccessfulShaItem{Branch: branch, LastSuccessfulSha: sha})
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Branch < items[j].Branch })
		*s = items
		return nil
	default:
		return fmt.Errorf("want JSON array or object, got %q", data[:min(len(data), 32)])
	}
}
