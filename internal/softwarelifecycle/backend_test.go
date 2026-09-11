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

package softwarelifecycle

import "testing"

func TestInventoryStateActivatable(t *testing.T) {
	for _, test := range []struct {
		state InventoryState
		want  bool
	}{
		{InventoryStatePresent, false},
		{InventoryStateInstalled, true},
		{InventoryStateInProgress, false},
		{InventoryStateProvisionedUncommitted, false},
		{InventoryStateProvisionedCommitted, false},
		{InventoryStateInvalid, false},
		{InventoryStateUnknown, false},
		{InventoryStateAbsent, false},
	} {
		if got := test.state.Activatable(); got != test.want {
			t.Errorf("%s.Activatable() = %t, want %t", test.state, got, test.want)
		}
	}
}
