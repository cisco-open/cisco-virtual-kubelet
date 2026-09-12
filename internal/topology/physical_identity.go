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

import (
	"fmt"
	"strings"
)

const MaxPhysicalIdentityLength = 128

// CanonicalPhysicalIdentity validates an operator- or device-supplied stable
// hardware identity and returns the case-insensitive canonical form used for
// duplicate detection and rollout admission. The accepted alphabet matches the
// CiscoDevice CRD and deliberately excludes whitespace and control characters.
func CanonicalPhysicalIdentity(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("physicalIdentity is required in managed topology")
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("physicalIdentity must not contain leading or trailing whitespace")
	}
	if len(value) > MaxPhysicalIdentityLength {
		return "", fmt.Errorf("physicalIdentity exceeds %d bytes", MaxPhysicalIdentityLength)
	}
	for index, character := range value {
		if asciiAlphaNumeric(character) {
			continue
		}
		if index == 0 || index == len(value)-1 || !strings.ContainsRune("._:/-", character) {
			return "", fmt.Errorf("physicalIdentity must start and end with an alphanumeric character and contain only alphanumerics, '.', '_', ':', '/', or '-'")
		}
	}
	return strings.ToLower(value), nil
}

// ObservedPhysicalIdentity requires the authenticated worker's live NodeInfo
// inventory to be internally consistent and to match the immutable declared
// authority. A worker observation can block admission, but never supplies or
// changes the identity used for deduplication.
func ObservedPhysicalIdentity(declared, machineID, systemUUID string) (string, error) {
	authority, err := CanonicalPhysicalIdentity(declared)
	if err != nil {
		return "", err
	}
	machineID = strings.TrimSpace(machineID)
	systemUUID = strings.TrimSpace(systemUUID)
	if machineID == "" && systemUUID == "" {
		return "", fmt.Errorf("Node has no live physical identity observation")
	}
	var observed string
	if machineID != "" {
		observed, err = CanonicalPhysicalIdentity(machineID)
		if err != nil {
			return "", fmt.Errorf("Node machineID is invalid: %w", err)
		}
	}
	if systemUUID != "" {
		canonicalSystem, systemErr := CanonicalPhysicalIdentity(systemUUID)
		if systemErr != nil {
			return "", fmt.Errorf("Node systemUUID is invalid: %w", systemErr)
		}
		if observed != "" && observed != canonicalSystem {
			return "", fmt.Errorf("Node physical identity observations conflict")
		}
		observed = canonicalSystem
	}
	if observed != authority {
		return "", fmt.Errorf("Node physical identity observation %q does not match declared authority %q", observed, authority)
	}
	return authority, nil
}

func asciiAlphaNumeric(character rune) bool {
	return character >= 'a' && character <= 'z' ||
		character >= 'A' && character <= 'Z' ||
		character >= '0' && character <= '9'
}
