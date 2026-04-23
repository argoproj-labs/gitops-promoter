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

// Package webrequestsimulator simulates a single WebRequestCommitStatus
// reconcile without making a real HTTP call. Supply a WebRequestCommitStatus
// (spec and optionally status), a PromotionStrategy, and a stand-in HTTPResponse;
// the simulator returns the rendered HTTP request, the rendered CommitStatus
// resources, and the Status the controller would have written.
//
// Because Result.Status mirrors WebRequestCommitStatus.Status exactly, it can
// be fed back into a subsequent Simulate() call via Input.WebRequestCommitStatus.Status
// to simulate a follow-up reconcile with the accumulated trigger/response/success outputs.
//
// The package is a thin adapter over internal/webrequest so external users can
// consume the simulator with a stable public type surface.
package webrequestsimulator
