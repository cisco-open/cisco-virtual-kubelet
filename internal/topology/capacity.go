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

import "fmt"

const (
	// MinManagedMaxPods and MaxManagedMaxPods bound the explicit Pod capacity
	// advertised by a managed virtual Node. The upper bound follows the
	// conventional kubelet ceiling and prevents an inventory typo from becoming
	// misleading scheduler capacity. Standalone compatibility is intentionally
	// outside this contract.
	MinManagedMaxPods int64 = 1
	MaxManagedMaxPods int64 = 110
)

// ValidateManagedMaxPods validates the Pod-capacity input used only by
// managed topology. The API server defaults an omitted spec.maxPods to 16, so
// zero reaching this boundary is an explicit invalid or un-defaulted value.
func ValidateManagedMaxPods(maxPods int64) error {
	if maxPods < MinManagedMaxPods || maxPods > MaxManagedMaxPods {
		return fmt.Errorf("managed topology requires spec.maxPods between %d and %d, got %d",
			MinManagedMaxPods, MaxManagedMaxPods, maxPods)
	}
	return nil
}
