// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "regexp"

// Publish only the bounded machine-code format used by Catalyst Center. Raw
// task failureReason, progress and data can contain credentials or device config.
var configurationTaskCode = regexp.MustCompile(`^NC[A-Z]{2}[0-9]{5}$`)

type configurationTaskFailure struct{ code string }

func newConfigurationTaskFailure(code string) *configurationTaskFailure {
	if !configurationTaskCode.MatchString(code) {
		code = ""
	}
	return &configurationTaskFailure{code: code}
}

func (e *configurationTaskFailure) Error() string {
	message := "Catalyst Center configuration task failed"
	if e.code != "" {
		message += " (" + e.code + ")"
	}
	if e.code == "NCSP11008" {
		message += "; required controller application unavailable"
	}
	return message + "; operation remains fenced for read-back recovery"
}
