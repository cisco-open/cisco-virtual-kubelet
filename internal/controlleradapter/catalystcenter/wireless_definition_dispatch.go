// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"slices"
)

// Compare typed native observations rather than serializing them: RF metadata
// intentionally has private fields. Readers own their freshly decoded objects;
// the executor must not mutate the captured observation. A nil selection protects
// the whole collection (RF default selection can affect other profiles).
func guardWirelessDefinitionWrite[T any](write *configurationWrite, before map[string]T, names []string, read func(context.Context) (map[string]T, error), desired func() any) error {
	names = slices.Clone(names)
	selectNative := func(in map[string]T) map[string]T {
		out := map[string]T{}
		if names == nil {
			for k, v := range in {
				out[k] = v
			}
		} else {
			for _, k := range names {
				if v, ok := in[k]; ok {
					out[k] = v
				}
			}
		}
		return out
	}
	planned := selectNative(before)
	intent, err := json.Marshal(desired())
	if err != nil {
		return errInvalidResponse
	}
	guardWirelessDefinitionPreflight(write, func(ctx context.Context) error {
		current, err := json.Marshal(desired())
		if err != nil {
			return errInvalidResponse
		}
		if !bytes.Equal(intent, current) {
			return errConfigurationStale
		}
		fresh, err := read(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(planned, selectNative(fresh)) {
			return errConfigurationStale
		}
		return nil
	})
	return nil
}

// Nil URLs and explicit empty URLs have different ownership semantics even
// though the native payload uses omitempty for the ordinary optional field.
func wirelessACLDispatchIntent(desired []wirelessACL) any {
	urls := make([][]string, len(desired))
	for i, v := range desired {
		urls[i] = v.URLs
	}
	return struct {
		Entries []wirelessACL
		URLs    [][]string
	}{desired, urls}
}

func wirelessRFDispatchIntent(desired []wirelessRFIntent) any {
	rows := make([]map[string]any, len(desired))
	for i, v := range desired {
		rows[i] = map[string]any{"name": v.name, "fields": v.fields}
	}
	return rows
}

// AP intent has private struct fields. Serialize the explicit public model and
// credential version selectors, never projected values or an empty struct.
func wirelessAPDispatchIntent(desired []wirelessAPIntent) any {
	rows := make([]map[string]any, len(desired))
	for i, want := range desired {
		var versions []map[string]any
		for _, credential := range want.credentials {
			versions = append(versions, map[string]any{"path": credential.path, "field": credential.field, "version": credential.version})
		}
		rows[i] = map[string]any{"name": want.name, "metadata": want.metadata, "credentials": versions}
	}
	return rows
}
