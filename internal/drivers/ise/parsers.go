// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

import (
	"regexp"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/common"
)

type iseApplicationStatus struct {
	Services map[string]string
}

var (
	iseVersionRe = regexp.MustCompile(`(?i)^\s*Version\s*[:=]\s*([0-9][0-9A-Za-z._-]*)`)
	iseProductRe = regexp.MustCompile(`(?i)Cisco\s+Identity\s+Services\s+Engine`)
	iseHostRe    = regexp.MustCompile(`(?i)^\s*(?:Hostname|Host Name)\s*[:=]\s*(\S+)`)
	iseSerialRe  = regexp.MustCompile(`(?i)^\s*(?:Serial Number|System Serial Number)\s*[:=]\s*(\S+)`)
)

func parseShowVersion(out string) *common.DeviceInfo {
	info := &common.DeviceInfo{ProductID: "Cisco Identity Services Engine"}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if info.ProductID == "" && iseProductRe.MatchString(trimmed) {
			info.ProductID = "Cisco Identity Services Engine"
		}
		if info.SoftwareVersion == "" {
			if m := iseVersionRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.SoftwareVersion = strings.TrimSpace(m[1])
			}
		}
		if info.Hostname == "" {
			if m := iseHostRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.Hostname = strings.TrimSpace(m[1])
			}
		}
		if info.SerialNumber == "" {
			if m := iseSerialRe.FindStringSubmatch(trimmed); len(m) > 1 {
				info.SerialNumber = strings.TrimSpace(m[1])
			}
		}
	}
	return info
}

func parseApplicationStatus(out string) iseApplicationStatus {
	status := iseApplicationStatus{Services: map[string]string{}}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "---") {
			continue
		}
		lower := strings.ToLower(trimmed)
		idx := strings.LastIndex(lower, "running")
		state := ""
		if idx >= 0 {
			state = "running"
		} else if strings.Contains(lower, "not running") || strings.Contains(lower, "stopped") {
			state = "stopped"
		}
		if state == "" {
			continue
		}
		name := strings.TrimSpace(trimmed[:strings.LastIndex(strings.ToLower(trimmed), state)])
		name = strings.TrimRight(name, ":")
		if name != "" {
			status.Services[name] = state
		}
	}
	return status
}

func applicationStatusHealthy(status iseApplicationStatus) bool {
	if len(status.Services) == 0 {
		return false
	}
	for _, state := range status.Services {
		if strings.ToLower(state) != "running" {
			return false
		}
	}
	return true
}
