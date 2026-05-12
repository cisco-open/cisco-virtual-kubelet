// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package softwareupgrade reconciles IOSXESoftwareUpgrade CRs through
// the multi-phase gNOI OS upgrade workflow: preflight → image fetch →
// streaming install → device-side validate → activate → wait for
// reachability → verify → terminal.
//
// The reconciler is deliberately one-step-per-call: every Reconcile
// observes Status.Phase, advances one transition, writes the new
// status, and requeues. This shape keeps each phase auditable, makes
// mid-flight delete handling straightforward (each phase declares
// what to do on deletion), and avoids long-running goroutines that
// would race the controller-runtime lifecycle.
package softwareupgrade
