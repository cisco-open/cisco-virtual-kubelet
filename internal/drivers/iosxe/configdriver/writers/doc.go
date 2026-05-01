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

// Package writers holds per-family adapters that translate between the
// netascode YAML shape of an IOSXEConfig and the RESTCONF/NETCONF
// operations that reach the device.
//
// Each family implements SectionWriter. A package-level registry exposes
// writers by family name so the config driver can iterate the ManagedFamilies
// of a CR without knowing about each family at compile time.
//
// Phase-0 scaffold: every writer returns ErrNotImplemented from Fetch, Diff
// and Apply. Registering the writer set is the load-bearing piece of this
// file set — the driver's reconcile loop can be exercised today and
// families light up one at a time in subsequent phases.
package writers
