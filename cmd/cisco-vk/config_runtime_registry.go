// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/client-go/rest"
)

type configRuntimeStarter func(context.Context, *rest.Config, string, configReconcilerOptions) error

var (
	configRuntimeMu       sync.RWMutex
	configRuntimeRegistry = map[ciskov1.DeviceDriver]configRuntimeStarter{}
)

func init() {
	registerConfigRuntime(ciskov1.DeviceDriverXE, startIOSXEConfigReconciler)
	registerConfigRuntime(ciskov1.DeviceDriverNXOS, startNXOSConfigReconciler)
}

func registerConfigRuntime(kind ciskov1.DeviceDriver, starter configRuntimeStarter) {
	if starter == nil {
		panic(fmt.Sprintf("registerConfigRuntime: nil starter for %q", kind))
	}
	configRuntimeMu.Lock()
	defer configRuntimeMu.Unlock()
	if _, dup := configRuntimeRegistry[kind]; dup {
		panic(fmt.Sprintf("registerConfigRuntime: duplicate starter for %q", kind))
	}
	configRuntimeRegistry[kind] = starter
}

func lookupConfigRuntime(kind ciskov1.DeviceDriver) (configRuntimeStarter, bool) {
	configRuntimeMu.RLock()
	defer configRuntimeMu.RUnlock()
	starter, ok := configRuntimeRegistry[kind]
	return starter, ok
}

func registeredConfigRuntimes() []ciskov1.DeviceDriver {
	configRuntimeMu.RLock()
	defer configRuntimeMu.RUnlock()
	out := make([]ciskov1.DeviceDriver, 0, len(configRuntimeRegistry))
	for kind := range configRuntimeRegistry {
		out = append(out, kind)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}
