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

// Package simulate implements the WebRequestCommitStatus reconcile simulator.
// It is internal to webrequestsimulator (see Go internal directory rules).
//
// Layout mirrors the controller and internal/webrequest split:
//   - reconcile.go — Simulate (vs controller Reconcile + webrequest Reconciler.ReconcileWebRequestCommitStatus*)
//   - http.go — mock HTTPEXecutor (vs reconciler Execute → makeHTTPRequest)
//   - commitstatus.go — renderCommitStatus (vs reconciler upsertCommitStatus)
package simulate
