// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/provider/diagnostic"
)

func TestWiredBulkBannerCLIRequiresCompleteExactBody(t *testing.T) {
	for _, mode := range []string{"inline", "multiline", "crlf", "raw-control", "printable", "whitespace", "empty", "different", "trimmed", "extra-newline", "missing-close", "duplicate", "trailing-cli", "wrong-kind", "partial", "unicode-delimiter", "unsupported-caret", "invalid-utf8", "builtin", "nul-source"} {
		t.Run(mode, func(t *testing.T) {
			want := networkSetting{kind: "banner", bannerType: "Custom", banner: "Authorized access"}
			output := "banner motd ^CAuthorized access^C\n"
			switch mode {
			case "multiline":
				want.banner = "\nAuthorized access\nSecond line\n"
				output = "banner motd ^C" + want.banner + "^C\n"
			case "crlf":
				want.banner = "\nAuthorized access\n"
				output = "\r\nbanner motd ^C\r\nAuthorized access\r\n^C\r\n"
			case "raw-control":
				output = "banner motd \x03Authorized access\x03\n"
			case "printable":
				output = "banner motd #Authorized access#\n"
			case "whitespace":
				want.banner = "  Authorized access  "
				output = "banner motd #" + want.banner + "#\n"
			case "empty":
				want.banner = ""
				output = "banner motd ##\n"
			case "different":
				output = "banner motd ^COther policy^C\n"
			case "trimmed":
				want.banner = " Authorized access "
			case "extra-newline":
				output = "banner motd ^CAuthorized access\n^C\n"
			case "missing-close":
				output = "banner motd ^CAuthorized access\n"
			case "duplicate":
				output += output
			case "trailing-cli":
				output += "hostname unexpected\n"
			case "wrong-kind":
				output = "banner login ^CAuthorized access^C\n"
			case "partial":
				output = "banner motd "
			case "unicode-delimiter":
				output = "banner motd §Authorized access§\n"
			case "unsupported-caret":
				output = "banner motd ^DAuthorized access^D\n"
			case "invalid-utf8":
				output = "banner motd #\xff#\n"
			case "builtin":
				want.bannerType = "Builtin"
			case "nul-source":
				want.banner = "\x00"
			}
			err := verifyWiredBulkNetworkCLI([]networkSetting{want}, []string{"Processor board ID SERIAL\n", output})
			valid := mode == "inline" || mode == "multiline" || mode == "crlf" || mode == "raw-control" || mode == "printable" || mode == "whitespace" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal("banner observation lost content or accepted incomplete output", err)
			}
		})
	}
}

func TestWiredBulkUTCClockRejectsImplicitOrSeasonalEquivalence(t *testing.T) {
	for _, mode := range []string{"utc", "etc-utc", "gmt", "etc-gmt", "minutes", "regional-zone", "seasonal-alias", "wrong-offset", "wrong-minutes", "wrong-label", "recurring-summer", "dated-summer", "missing", "duplicate", "bad-fields", "unrelated-clock", "status-only"} {
		t.Run(mode, func(t *testing.T) {
			want := networkSetting{kind: "timezone", timezone: "UTC"}
			output := "clock timezone UTC 0\n"
			switch mode {
			case "etc-utc":
				want.timezone = "Etc/UTC"
			case "gmt":
				want.timezone, output = "GMT", "clock timezone GMT 0\n"
			case "etc-gmt":
				want.timezone = "Etc/GMT"
			case "minutes":
				output = "clock timezone UTC 0 0\n"
			case "regional-zone":
				want.timezone = "Europe/London"
			case "seasonal-alias":
				want.timezone = "Africa/Abidjan"
			case "wrong-offset":
				output = "clock timezone UTC 1\n"
			case "wrong-minutes":
				output = "clock timezone UTC 0 30\n"
			case "wrong-label":
				output = "clock timezone LOCAL 0\n"
			case "recurring-summer":
				output += "clock summer-time SUMMER recurring\n"
			case "dated-summer":
				output += "clock summer-time SUMMER date 1 Jan 2026 0:00 2 Jan 2026 0:00\n"
			case "missing":
				output = "\n"
			case "duplicate":
				output += output
			case "bad-fields":
				output = "clock timezone UTC 0 0 unexpected\n"
			case "unrelated-clock":
				output += "clock calendar-valid\n"
			case "status-only":
				output = "UTC COMPLIANT\n"
			}
			err := verifyWiredBulkNetworkCLI([]networkSetting{want}, []string{"Processor board ID SERIAL\n", output})
			valid := mode == "utc" || mode == "etc-utc" || mode == "gmt" || mode == "etc-gmt" || mode == "minutes"
			if (err == nil) != valid {
				t.Fatal("clock observation inferred unsupported timezone semantics", err)
			}
		})
	}
}

func TestWiredBulkNetworkQueriesUseExistingWorkerPolicy(t *testing.T) {
	wants := []networkSetting{{kind: "banner", bannerType: "Custom", banner: "private policy value"}, {kind: "dns", domain: "example.test", servers: []string{"192.0.2.53"}}, {kind: "ntp", servers: []string{"192.0.2.1"}}, {kind: "timezone", timezone: "Etc/UTC"}}
	commands, err := wiredBulkNetworkCommands(wants)
	if err != nil {
		t.Fatal(err)
	}
	if err := diagnostic.ValidateCommandsForPlatform(diagnostic.CommandPlatformIOSXE, commands); err != nil {
		t.Fatal("generated query conflicts with the existing worker allowlist", err)
	}
	for _, command := range commands {
		if strings.Contains(command, "private policy") || strings.Contains(command, "192.0.2.") {
			t.Fatal("source content was inserted into a command")
		}
	}
}
