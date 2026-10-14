// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// collectScalarReferences records the API locations containing schema-valid
// placeholders. The client replaces them with observed IDs before every write.
func collectScalarReferences(def scalarDefinition, input map[string]any, prefix []string) []ParcelReference {
	var refs []ParcelReference
	for public, field := range def.Fields {
		if field.Source != "" {
			public = field.Source
		}
		if field.Reference == "" || input[public] == nil {
			continue
		}
		name, _ := input[public].(string)
		path := append(append([]string{}, prefix...), strings.Split(field.Path, ".")...)
		refs = append(refs, ParcelReference{Kind: field.ReferenceKind, Name: name, ParcelPath: field.Reference, BodyPath: append(path, "value")})
	}
	for _, wrapper := range def.Wrappers {
		if wrapper.Nonempty != "" && (input[wrapper.Nonempty] == nil || input[wrapper.Nonempty] == "") {
			continue
		}
		path := append(append([]string{}, prefix...), strings.Split(wrapper.Path, ".")...)
		refs = append(refs, collectScalarReferences(wrapper.Definition, input, append(path, "0"))...)
	}
	for public, list := range def.Lists {
		items, _ := input[public].([]any)
		for i, item := range items {
			object, _ := item.(map[string]any)
			if list.Primitive {
				object = map[string]any{"$value": item}
			}
			path := append(append([]string{}, prefix...), strings.Split(list.Path, ".")...)
			refs = append(refs, collectScalarReferences(list.Definition, object, append(path, strconv.Itoa(i)))...)
		}
	}
	sort.Slice(refs, func(i, j int) bool { return strings.Join(refs[i].BodyPath, "/") < strings.Join(refs[j].BodyPath, "/") })
	return refs
}

// orderParcelDependencies requires references to be declared in the same profile.
// Stable topological ordering supports chains, not just leaves followed by groups.
func orderParcelDependencies(resources []Resource) ([]Resource, error) {
	identity := func(r Resource) string {
		return r.Collection + "\x00" + r.ParentName + "\x00" + r.ParcelPath + "\x00" + r.Name
	}
	indices := map[string]int{}
	for i, r := range resources {
		if r.Kind == "parcel" || r.Kind == "association" {
			key := identity(r)
			if _, exists := indices[key]; exists {
				return nil, errors.New("duplicate parcel identity")
			}
			indices[key] = i
		}
	}
	dependencies := make([][]int, len(resources))
	for i, r := range resources {
		if len(r.ParcelAncestors) > 0 {
			last := r.ParcelAncestors[len(r.ParcelAncestors)-1]
			parent := r
			parent.Name = last.Name
			parent.ParcelPath = last.Path
			j, exists := indices[identity(parent)]
			if !exists {
				return nil, errors.New("nested parcel parent is not declared")
			}
			dependencies[i] = append(dependencies[i], j)
		} else if r.ParentParcelPath != "" {
			parent := r
			parent.Name = r.ParentParcelName
			parent.ParcelPath = r.ParentParcelPath
			j, exists := indices[identity(parent)]
			if !exists {
				return nil, errors.New("nested parcel parent is not declared")
			}
			dependencies[i] = append(dependencies[i], j)
		}
		if r.HubSpoke != nil && r.HubSpoke.UUIDMode {
			for _, site := range r.HubSpoke.names() {
				found := -1
				for j, candidate := range resources {
					if candidate.Kind == "hierarchy" && candidate.Type == "site" && candidate.Name == site {
						found = j
						break
					}
				}
				if found < 0 {
					return nil, errors.New("hub-spoke site is not declared")
				}
				dependencies[i] = append(dependencies[i], found)
			}
		}
		for _, site := range r.HierarchySites {
			if r.Type != "topology_mesh" && r.Type != "topology_custom_control" {
				continue
			}
			found := -1
			for j, candidate := range resources {
				if candidate.Kind == "hierarchy" && candidate.Type == "site" && candidate.Name == site {
					found = j
					break
				}
			}
			if found < 0 {
				return nil, errors.New("topology site is not declared")
			}
			dependencies[i] = append(dependencies[i], found)
		}
		for _, ref := range r.ParcelRefs {
			target := r
			target.Name = ref.Name
			target.ParcelPath = ref.ParcelPath
			if ref.Collection != "" {
				target.Collection = ref.Collection
			}
			if ref.ParentName != "" {
				target.ParentName = ref.ParentName
			}
			j, exists := indices[identity(target)]
			if !exists || (ref.Kind != "" && resources[j].Type != ref.Kind) {
				return nil, fmt.Errorf("parcel reference %s/%s is not declared in the required collection of this profile", ref.ParcelPath, ref.Name)
			}
			dependencies[i] = append(dependencies[i], j)
		}
	}
	out := make([]Resource, 0, len(resources))
	done := make([]bool, len(resources))
	for len(out) < len(resources) {
		progress := false
		for i, r := range resources {
			if done[i] {
				continue
			}
			ready := true
			for _, j := range dependencies[i] {
				if !done[j] {
					ready = false
					break
				}
			}
			if ready {
				out = append(out, r)
				done[i] = true
				progress = true
			}
		}
		if !progress {
			return nil, errors.New("cyclic parcel references")
		}
	}
	return out, nil
}
