// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"net/netip"
)

// validateIPAMAllocation checks the complete allocation graph before execution
// can release a write. Existing allocations outside this manifest still count.
// Pool replacement, resizing and reservation relocation require a separate,
// explicit workflow; a PUT must never silently implement them.
func validateIPAMAllocation(intent networkReportIntent, pools []ipPool, reservations []ipReservation) error {
	byName := map[string]ipPool{}
	byID := map[string]ipPool{}
	for _, p := range pools {
		if !validAPIID(p.ID) || !validPoolName(p.Name) {
			return errInvalidResponse
		}
		if _, exists := byName[p.Name]; exists {
			return errInvalidResponse
		}
		if _, exists := byID[p.ID]; exists {
			return errInvalidResponse
		}
		byName[p.Name], byID[p.ID] = p, p
	}
	for _, p := range intent.pools {
		if old, exists := byName[p.Name]; exists {
			before, e1 := poolPrefix(old.Space)
			after, e2 := poolPrefix(p.Space)
			if e1 != nil || e2 != nil || before != after || old.Type != p.Type {
				return errors.New("IPAM pool replacement or resizing is not supported")
			}
		} else {
			byName[p.Name] = p
		}
	}
	allocated := []netip.Prefix{}
	for _, p := range byName {
		prefix, err := poolPrefix(p.Space)
		if err != nil {
			return err
		}
		for _, other := range allocated {
			if prefix.Overlaps(other) {
				return errors.New("IPAM global pools overlap")
			}
		}
		allocated = append(allocated, prefix)
	}
	type allocation struct {
		name, parent string
		prefix       netip.Prefix
	}
	children := []allocation{}
	appendChild := func(name, parent string, space *poolAddressSpace) error {
		pool, ok := byName[parent]
		if !ok || pool.Type == "Tunnel" {
			return errors.New("IPAM reservation parent is missing or incompatible")
		}
		outer, err := poolPrefix(pool.Space)
		if err != nil {
			return err
		}
		prefix, err := poolPrefix(space)
		if err != nil {
			return err
		}
		if prefix.Bits() < outer.Bits() || !outer.Contains(prefix.Addr()) {
			return errors.New("IPAM reservation is outside its parent pool")
		}
		for _, old := range children {
			if old.parent == parent && old.prefix.Overlaps(prefix) {
				return errors.New("IPAM reservations overlap")
			}
		}
		children = append(children, allocation{name, parent, prefix})
		return nil
	}
	// Reconcile existing reservations by name because NetAsCode reservation names
	// are unique across its site assignments. Ambiguous appliance names fail closed.
	actual := map[string]ipReservation{}
	for _, r := range reservations {
		if _, exists := actual[r.Name]; exists {
			return errors.New("IPAM reservation name is ambiguous")
		}
		actual[r.Name] = r
		for _, s := range []*poolAddressSpace{r.V4, r.V6} {
			if s == nil {
				continue
			}
			parent, ok := byID[s.GlobalPoolID]
			if !ok {
				return errInvalidResponse
			}
			if err := appendChild(r.Name, parent.Name, s); err != nil {
				return err
			}
		}
	}
	for _, r := range intent.reservations {
		old, exists := actual[r.Name]
		if exists && old.Type != r.Type {
			return errors.New("IPAM reservation type replacement is not supported")
		}
		for i, s := range []*poolAddressSpace{r.V4, r.V6} {
			if s == nil {
				continue
			}
			if exists {
				previous := old.V4
				if i == 1 {
					previous = old.V6
				}
				if previous != nil {
					before, e1 := poolPrefix(previous)
					after, e2 := poolPrefix(s)
					parent, ok := byID[previous.GlobalPoolID]
					if !ok || e1 != nil || e2 != nil || before != after || parent.Name != s.GlobalPoolID {
						return errors.New("IPAM reservation resizing or reparenting is not supported")
					}
					continue
				}
			}
			if err := appendChild(r.Name, s.GlobalPoolID, s); err != nil {
				return err
			}
		}
	}
	return nil
}
