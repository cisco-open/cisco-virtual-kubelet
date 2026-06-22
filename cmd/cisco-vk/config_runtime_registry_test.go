// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"sync"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestConfigRuntimeRegistryHasDeviceCentricPlatforms(t *testing.T) {
	for _, driver := range []ciskov1.DeviceDriver{
		ciskov1.DeviceDriverXE,
		ciskov1.DeviceDriverNXOS,
	} {
		if _, ok := lookupConfigRuntime(driver); !ok {
			t.Fatalf("config runtime for %s is not registered; registered=%v", driver, registeredConfigRuntimes())
		}
	}
}

func TestConfigDriverBuildOptionsCarriesSessionLock(t *testing.T) {
	lock := &sync.Mutex{}
	opts := configDriverBuildOptions(configReconcilerOptions{SessionLock: lock})
	if opts.SessionLock != lock {
		t.Fatalf("SessionLock=%p, want %p", opts.SessionLock, lock)
	}
}
