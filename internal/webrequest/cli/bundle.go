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

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
	"gopkg.in/yaml.v3"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigyaml "sigs.k8s.io/yaml"
)

const yamlDocSeparator = "\n---\n"

// LoadBundle parses a multi-document YAML file into simulatortypes.Input.
//
// Documents may appear in any order. Exactly one WebRequestCommitStatus and one
// PromotionStrategy are required. At most one SimulatorConfig is allowed.
func LoadBundle(bundleYAML []byte) (*simulatortypes.Input, error) {
	docs := splitYAMLDocuments(bundleYAML)
	var wrcs *promoterv1alpha1.WebRequestCommitStatus
	var ps *promoterv1alpha1.PromotionStrategy
	var sim *SimulatorConfig

	for i, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		kind, err := peekYAMLKind(doc)
		if err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}
		if kind == "" {
			// Allow comment-only preambles (e.g. "# Example bundle" before the first "---").
			continue
		}

		switch kind {
		case "WebRequestCommitStatus":
			if wrcs != nil {
				return nil, fmt.Errorf("document %d: duplicate WebRequestCommitStatus", i+1)
			}
			o, err := decodeWebRequestCommitStatus(doc)
			if err != nil {
				return nil, fmt.Errorf("document %d (WebRequestCommitStatus): %w", i+1, err)
			}
			wrcs = o
		case "PromotionStrategy":
			if ps != nil {
				return nil, fmt.Errorf("document %d: duplicate PromotionStrategy", i+1)
			}
			o, err := decodePromotionStrategy(doc)
			if err != nil {
				return nil, fmt.Errorf("document %d (PromotionStrategy): %w", i+1, err)
			}
			ps = o
		case SimulatorConfigKind:
			if sim != nil {
				return nil, fmt.Errorf("document %d: duplicate %s", i+1, SimulatorConfigKind)
			}
			o, err := decodeSimulatorConfig(doc)
			if err != nil {
				return nil, fmt.Errorf("document %d (%s): %w", i+1, SimulatorConfigKind, err)
			}
			sim = o
		default:
			return nil, fmt.Errorf("document %d: unsupported kind %q (expected WebRequestCommitStatus, PromotionStrategy, or %s)",
				i+1, kind, SimulatorConfigKind)
		}
	}

	if wrcs == nil {
		return nil, errors.New("bundle must contain exactly one WebRequestCommitStatus")
	}
	if ps == nil {
		return nil, errors.New("bundle must contain exactly one PromotionStrategy")
	}

	in := &simulatortypes.Input{
		WebRequestCommitStatus: wrcs,
		PromotionStrategy:      ps,
	}
	if sim != nil {
		if sim.NamespaceMetadata != nil {
			in.NamespaceMetadata = *sim.NamespaceMetadata
		}
		in.HTTPResponses = sim.HTTPResponses
	}
	return in, nil
}

func splitYAMLDocuments(raw []byte) [][]byte {
	s := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	parts := bytes.Split(s, []byte(yamlDocSeparator))
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		p = bytes.TrimSpace(p)
		p = bytes.TrimPrefix(p, []byte("---"))
		p = bytes.TrimSpace(p)
		if len(p) > 0 {
			out = append(out, p)
		}
	}
	return out
}

func peekYAMLKind(doc []byte) (string, error) {
	var meta metav1.TypeMeta
	if err := yaml.Unmarshal(doc, &meta); err != nil {
		return "", fmt.Errorf("peek kind: %w", err)
	}
	return strings.TrimSpace(meta.Kind), nil
}

func decodeWebRequestCommitStatus(doc []byte) (*promoterv1alpha1.WebRequestCommitStatus, error) {
	jsonDoc, err := sigyaml.YAMLToJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("yaml to json: %w", err)
	}
	var o promoterv1alpha1.WebRequestCommitStatus
	if err := json.Unmarshal(jsonDoc, &o); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &o, nil
}

func decodePromotionStrategy(doc []byte) (*promoterv1alpha1.PromotionStrategy, error) {
	jsonDoc, err := sigyaml.YAMLToJSON(doc)
	if err != nil {
		return nil, fmt.Errorf("yaml to json: %w", err)
	}
	var o promoterv1alpha1.PromotionStrategy
	if err := json.Unmarshal(jsonDoc, &o); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &o, nil
}

func decodeSimulatorConfig(doc []byte) (*SimulatorConfig, error) {
	var o SimulatorConfig
	if err := yaml.Unmarshal(doc, &o); err != nil {
		return nil, fmt.Errorf("decode SimulatorConfig: %w", err)
	}
	return &o, nil
}
