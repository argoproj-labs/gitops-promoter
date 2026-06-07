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

package notification

// Shared string constants used across the notification controller package tests
// (delivery_test.go, e2e_test.go, promoternotification_controller_test.go). Centralizing
// them keeps goconst happy and avoids typo drift between the suites.
const (
	testNamespace        = "ns-a"
	testAPIVersion       = "promoter.argoproj.io/v1alpha1"
	testKindCTP          = "ChangeTransferPolicy"
	testSigningSecret    = "sign-secret"
	testSigningKey       = "hmac"
	testCustomHeaderVal  = "abc"
	testCustomHeaderName = "X-Custom"
	testTeamLabelValue   = "payments"
)
