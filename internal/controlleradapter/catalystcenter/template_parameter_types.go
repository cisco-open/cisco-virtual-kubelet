// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

func validTemplateParameterDataType(kind string) bool {
	switch kind {
	case "STRING", "INTEGER", "IPADDRESS", "MACADDRESS", "SECTIONDIVIDER":
		return true
	default:
		return false
	}
}
