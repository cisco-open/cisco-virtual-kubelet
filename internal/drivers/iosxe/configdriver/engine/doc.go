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

// Package engine is the per-device reconcile state machine that turns a
// ResolvedIntent into device-facing transport.Ops via the registered
// SectionWriters.
//
// The engine is deliberately separate from the polling reconciler in
// internal/provider: the reconciler schedules work, the engine executes
// one iteration of it. Separation keeps the engine fully unit-testable
// without any Kubernetes informers or tickers.
package engine
