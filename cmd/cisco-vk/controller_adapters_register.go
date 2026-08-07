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

package main

// Concrete network-controller adapters are enabled with blank imports in this
// composition file, mirroring drivers_register.go. The generic manager reads
// only their registered descriptors; controller-worker is the sole command
// that constructs a registered factory.
//
// Keep product imports out of internal/controlleradapter and internal/controller so
// removing an adapter never changes the neutral API, registry, orchestration,
// or the existing per-device transport graph.
