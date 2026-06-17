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

// Package configengine is the platform-neutral import root for declarative
// configuration reconciliation.
//
// The first implementation slice keeps the mature IOS-XE engine as the
// backing implementation and exposes it through neutral subpackages. This
// gives new platforms a stable contract while the generated IOS-XE writer tree
// is moved behind the same API in smaller follow-up changes.
package configengine
