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

package topologyhealth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// AcceptedNetworkDigest identifies the complete accepted observation wire
// value. Device observations contain ordered slices and no maps, so ordinary
// JSON encoding is deterministic while retaining every provenance and health
// input the manager accepted.
func AcceptedNetworkDigest(observation *ciskov1.DeviceNetworkObservationStatus) (string, error) {
	if observation == nil {
		return "", fmt.Errorf("accepted network observation is absent")
	}
	raw, err := json.Marshal(observation)
	if err != nil {
		return "", fmt.Errorf("encode accepted network observation: %w", err)
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
