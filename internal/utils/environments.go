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

package utils

import (
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// GetApplicableEnvironments returns PromotionStrategy spec environments whose global or
// per-environment commit-status selectors include key. reportOn selects which set of
// selectors to check: constants.CommitRefActive uses ActiveCommitStatuses; any other
// value (including constants.CommitRefProposed or empty string) uses ProposedCommitStatuses.
func GetApplicableEnvironments(ps *promoterv1alpha1.PromotionStrategy, key string, reportOn string) []promoterv1alpha1.Environment {
	globalSelectors := ps.Spec.ProposedCommitStatuses
	getEnvSelectors := func(e promoterv1alpha1.Environment) []promoterv1alpha1.CommitStatusSelector {
		return e.ProposedCommitStatuses
	}
	if reportOn == constants.CommitRefActive {
		globalSelectors = ps.Spec.ActiveCommitStatuses
		getEnvSelectors = func(e promoterv1alpha1.Environment) []promoterv1alpha1.CommitStatusSelector {
			return e.ActiveCommitStatuses
		}
	}

	keyInSelectors := func(selectors []promoterv1alpha1.CommitStatusSelector) bool {
		for _, sel := range selectors {
			if sel.Key == key {
				return true
			}
		}
		return false
	}
	keyInGlobal := keyInSelectors(globalSelectors)

	applicable := make([]promoterv1alpha1.Environment, 0, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		if keyInGlobal || keyInSelectors(getEnvSelectors(env)) {
			applicable = append(applicable, env)
		}
	}
	return applicable
}

// DetermineCommitStatusReportOn parses the passed PromotionStrategy and searches for the provided key in both
// activeCommitStatuses and proposedCommitStatuses to see which type to use before selecting environments.
// If the key is not found in the PromotionStrategy an error will be returned. If the key is found in both
// active and proposed an error will also be returned.
func DetermineReportOn(ps *promoterv1alpha1.PromotionStrategy, key string) (string, error) {
	reportOn := ""
	found := false

	parseCommitStatuses := func(statuses []promoterv1alpha1.CommitStatusSelector, setTo string) error {
		for _, pcs := range statuses {
			if key == pcs.Key && !found {
				reportOn = setTo
				found = true
				break
			} else if key == pcs.Key && found && reportOn != setTo {
				return fmt.Errorf("key %s was found as both a proposed and active commit status", key)
			}
		}
		return nil
	}

	if err := parseCommitStatuses(ps.Spec.ProposedCommitStatuses, constants.CommitRefProposed); err != nil {
		return "", err
	}

	if err := parseCommitStatuses(ps.Spec.ActiveCommitStatuses, constants.CommitRefActive); err != nil {
		return "", err
	}

	for _, env := range ps.Spec.Environments {
		if err := parseCommitStatuses(env.ProposedCommitStatuses, constants.CommitRefProposed); err != nil {
			return "", err
		}

		if err := parseCommitStatuses(env.ActiveCommitStatuses, constants.CommitRefActive); err != nil {
			return "", err
		}
	}

	if reportOn == "" {
		return "", fmt.Errorf("key %s was not found in PromotionStrategy %s/%s", key, ps.Namespace, ps.Name)
	}

	return reportOn, nil
}
