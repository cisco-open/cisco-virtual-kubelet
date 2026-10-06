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

package topology

import "github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"

const MaxPhysicalIdentityLength = topologyidentity.MaxPhysicalIdentityLength

// CanonicalPhysicalIdentity preserves the controller-facing API while its
// dependency-free implementation is shared with release tooling.
func CanonicalPhysicalIdentity(value string) (string, error) {
	return topologyidentity.CanonicalPhysicalIdentity(value)
}

// ObservedPhysicalIdentity preserves the controller-facing API while its
// dependency-free implementation is shared with release tooling.
func ObservedPhysicalIdentity(declared, machineID, systemUUID string) (string, error) {
	return topologyidentity.ObservedPhysicalIdentity(declared, machineID, systemUUID)
}
