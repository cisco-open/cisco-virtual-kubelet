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

package nexusdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"

	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
)

const (
	deviceNamePrefix = "nd-"
	maxNameLen       = 63
)

// physicalIdentityRE mirrors CiscoDevice.spec.physicalIdentity validation, so
// an unusual serial from ND is skipped instead of failing admission forever.
var physicalIdentityRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._:/-]{0,126}[A-Za-z0-9])?$`)

// deviceName derives a stable CiscoDevice name from the switch serial, which
// ND treats as the switch identity. Characters outside [a-z0-9-] become '-';
// names that would exceed the Node-name limit are shortened with a hash so
// two long serials cannot collide. It returns false when nothing usable
// remains.
func deviceName(serial string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(serial) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	core := strings.Trim(b.String(), "-")
	if core == "" {
		return "", false
	}
	name := deviceNamePrefix + core
	if len(name) > maxNameLen {
		sum := sha256.Sum256([]byte(serial))
		name = strings.TrimRight(name[:maxNameLen-9], "-") + "-" + hex.EncodeToString(sum[:])[:8]
	}
	return name, len(utilvalidation.IsDNS1123Subdomain(name)) == 0
}

// nodeNameFromHostname returns a valid Node name derived from an ND hostname,
// or "" when the hostname cannot be used as-is. Nothing is rewritten: a
// hostname that is not already a lowercase DNS-1123 name is not guessed at,
// because spec.nodeName is immutable and a wrong guess would be permanent.
func nodeNameFromHostname(hostname string) string {
	h := strings.TrimSpace(hostname)
	if h == "" || len(h) > maxNameLen || len(utilvalidation.IsDNS1123Subdomain(h)) > 0 {
		return ""
	}
	return h
}

// labelValue returns v when it is a valid, non-empty label value.
func labelValue(v string) string {
	if v == "" || len(utilvalidation.IsValidLabelValue(v)) > 0 {
		return ""
	}
	return v
}
