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

// Package schema exposes the hand-maintained family index and the set of
// currently supported Cisco-IOS-XE YANG releases.
//
// families.yaml is the contract between the netascode YAML shape (keyed
// by family name, e.g. "vlan", "interface_ethernet") and the YANG xpaths
// a writer reads from and writes to. It is deliberately separate from
// the generated YANG Go types so the human-authored mapping survives
// YANG-release bumps without a diff in the tracked mapping file.
//
// yang-versions.yaml lists the releases the driver ships writers for.
// The schema-sync tool (tools/cisco-vk-yang-sync) reads both files.
package schema
