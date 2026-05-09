// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mapper

import (
	"sort"
	"strings"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
)

// ListKeyTuple is the ordered list-key trail along a flattened path.
type ListKeyTuple []ListKey

type ListKey struct {
	ListPath string
	KeyName  string
	KeyValue string
}

// FlattenPath joins a gNMI Notification prefix with an update/delete path using
// gNMI's prefix-relative semantics. PathElem order is preserved from the wire.
func FlattenPath(prefix, path *gpb.Path) (canonical string, keys []KeyValue, originalOrder bool, tuple ListKeyTuple) {
	var elems []*gpb.PathElem
	if prefix != nil {
		elems = append(elems, prefix.GetElem()...)
	}
	if path != nil {
		elems = append(elems, path.GetElem()...)
	}
	parts := make([]string, 0, len(elems))
	listPathParts := make([]string, 0, len(elems))
	for _, elem := range elems {
		if elem == nil || elem.Name == "" {
			continue
		}
		name := stripOriginPrefix(elem.Name)
		part := name
		listPathParts = append(listPathParts, name)
		listPath := "/" + strings.Join(listPathParts, "/")
		if len(elem.Key) > 0 {
			keyNames := make([]string, 0, len(elem.Key))
			for k := range elem.Key {
				keyNames = append(keyNames, k)
			}
			sort.Strings(keyNames)
			for _, k := range keyNames {
				v := elem.Key[k]
				part += "[" + k + "=" + v + "]"
				keys = append(keys, KeyValue{Key: k, Value: v})
				tuple = append(tuple, ListKey{ListPath: listPath, KeyName: k, KeyValue: v})
			}
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "/", keys, true, tuple
	}
	return "/" + strings.Join(parts, "/"), keys, true, tuple
}

func normalizeCanonicalPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	p = strings.TrimPrefix(p, "/")
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = stripOriginPrefix(parts[i])
	}
	return "/" + strings.Join(parts, "/")
}

func stripOriginPrefix(elem string) string {
	if idx := strings.Index(elem, ":"); idx >= 0 {
		return elem[idx+1:]
	}
	return elem
}

func pathOrigin(prefix, path *gpb.Path) string {
	if path != nil && path.Origin != "" {
		return path.Origin
	}
	if prefix != nil {
		return prefix.Origin
	}
	return ""
}
