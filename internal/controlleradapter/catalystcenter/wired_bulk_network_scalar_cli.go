// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "strings"

// Fixed UTC aliases need no platform-specific daylight-saving translation.
// Equal offsets today cannot establish equivalence between arbitrary IANA zones.
func wiredBulkFixedUTCZone(zone string) bool {
	switch zone {
	case "UTC", "Etc/UTC", "GMT", "Etc/GMT":
		return true
	default:
		return false
	}
}

func verifyWiredBulkUTCClockCLI(output string) error {
	timezones := 0
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) >= 2 && fields[0] == "clock" && fields[1] == "summer-time" {
			return errConfigurationStale
		}
		if len(fields) != 4 && len(fields) != 5 {
			return errInvalidResponse
		}
		if fields[0] != "clock" || fields[1] != "timezone" {
			return errInvalidResponse
		}
		timezones++
		if timezones != 1 || fields[2] != "UTC" && fields[2] != "GMT" || fields[3] != "0" || len(fields) == 5 && fields[4] != "0" {
			return errConfigurationStale
		}
	}
	// Query default-expanded configuration, but never infer UTC if this platform
	// still omits the timezone, the filter failed, or the read is incomplete.
	if timezones != 1 {
		return errConfigurationBusy
	}
	return nil
}

// Compare only a complete MOTD block, preserving body whitespace. CRLF is the
// transport spelling of a line ending; no banner-body trim or token expansion
// is performed. Unsupported display forms remain unverified.
func verifyWiredBulkBannerCLI(want, output string) error {
	if !validBanner(want) {
		return errWiredBulkNetworkContextUnavailable
	}
	text := strings.TrimLeft(strings.ReplaceAll(output, "\r\n", "\n"), " \t\n")
	text, ok := strings.CutPrefix(text, "banner motd ")
	if !ok || text == "" {
		return errInvalidResponse
	}
	delimiter := ""
	switch {
	case strings.HasPrefix(text, "^C"):
		delimiter = "^C"
	case text[0] == 3:
		delimiter = "\x03"
	case text[0] >= 33 && text[0] <= 126 && text[0] != '^':
		delimiter = text[:1]
	default:
		return errInvalidResponse
	}
	text = text[len(delimiter):]
	end := strings.Index(text, delimiter)
	if end < 0 {
		return errInvalidResponse
	}
	message, suffix := text[:end], text[end+len(delimiter):]
	if !validBanner(message) || strings.Trim(suffix, " \t\n") != "" {
		return errInvalidResponse
	}
	if message != strings.ReplaceAll(want, "\r\n", "\n") {
		return errConfigurationStale
	}
	return nil
}
