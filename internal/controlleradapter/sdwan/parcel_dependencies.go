// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"errors"
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
		if r.Kind == "parcel" {
			key := identity(r)
			if _, exists := indices[key]; exists {
				return nil, errors.New("duplicate parcel identity")
			}
			indices[key] = i
		}
	}
	dependencies := make([][]int, len(resources))
	for i, r := range resources {
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
				return nil, errors.New("parcel reference is not declared in the required collection of this profile")
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
