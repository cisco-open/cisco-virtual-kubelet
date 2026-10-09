// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package softwarelifecycle

import "fmt"

// UpgradeMethod selects who performs an upgrade. Image inventory Backend is
// deliberately separate: having native inventory does not select an executor.
type UpgradeMethod string

const (
	UpgradeDirect         UpgradeMethod = "Direct"
	UpgradeCatalystCenter UpgradeMethod = "CatalystCenter"
)

// UpgradeExecution binds an operation to one executor. Direct is the default;
// a controller route must name an exact, resolved controller incarnation.
// This is an internal execution contract, not a new Kubernetes API.
type UpgradeExecution struct {
	Method              UpgradeMethod
	ControllerNamespace string
	ControllerName      string
	ControllerUID       string
}

func (e UpgradeExecution) Resolve() (UpgradeExecution, error) {
	if e.Method == "" {
		e.Method = UpgradeDirect
	}
	switch e.Method {
	case UpgradeDirect:
		if e.ControllerNamespace != "" || e.ControllerName != "" || e.ControllerUID != "" {
			return UpgradeExecution{}, fmt.Errorf("Direct execution cannot reference a controller")
		}
	case UpgradeCatalystCenter:
		if e.ControllerNamespace == "" || e.ControllerName == "" || e.ControllerUID == "" {
			return UpgradeExecution{}, fmt.Errorf("CatalystCenter execution requires a resolved controller namespace, name and UID")
		}
	default:
		return UpgradeExecution{}, fmt.Errorf("unsupported upgrade execution method %q", e.Method)
	}
	return e, nil
}

// ValidatePinned rejects changing executors after planning, including changing
// a controller with the same name to a different Kubernetes incarnation. A
// failed controller request never authorizes fallback to Direct execution.
func (e UpgradeExecution) ValidatePinned(pinned UpgradeExecution) error {
	resolved, err := e.Resolve()
	if err != nil {
		return err
	}
	prior, err := pinned.Resolve()
	if err != nil {
		return err
	}
	if resolved != prior {
		return fmt.Errorf("upgrade execution binding is immutable; create a separately authorized operation")
	}
	return nil
}
