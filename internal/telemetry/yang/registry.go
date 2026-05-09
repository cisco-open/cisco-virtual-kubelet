// Copyright 2026 Cisco Systems Inc.
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

package yang

import (
	"sort"
	"strings"
	"sync"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
	goyang "github.com/openconfig/goyang/pkg/yang"
)

type TypeInfo struct {
	ModuleName   string
	ModulePrefix string
	LeafPath     string
	TypeName     string
	Kind         classifier.MetricKind
}

type Registry struct {
	mu sync.RWMutex

	modules    map[string]string
	byKey      map[lookupKey]TypeInfo
	byLeafPath map[string]TypeInfo
	ambiguous  map[string]struct{}
	cache      *Cache
}

func newRegistryFromModules(modules *goyang.Modules) *Registry {
	reg := &Registry{
		modules:    map[string]string{},
		byKey:      map[lookupKey]TypeInfo{},
		byLeafPath: map[string]TypeInfo{},
		ambiguous:  map[string]struct{}{},
		cache:      NewCache(DefaultCacheCapacity),
	}
	names := make([]string, 0, len(modules.Modules))
	for name := range modules.Modules {
		if strings.Contains(name, "@") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mod := modules.Modules[name]
		if mod == nil {
			continue
		}
		entry := goyang.ToEntry(mod)
		prefix := mod.GetPrefix()
		reg.modules[mod.Name] = prefix
		reg.walkModule(entry, mod.Name, prefix)
	}
	return reg
}

func (r *Registry) Lookup(canonicalPath string) (classifier.MetricKind, bool) {
	if r == nil {
		return "", false
	}
	modulePrefix, leafPath := splitCanonicalPath(canonicalPath)
	if kind, ok := r.cache.Get(modulePrefix, leafPath); ok {
		return kind, true
	}

	r.mu.RLock()
	info, ok := r.byKey[lookupKey{modulePrefix: modulePrefix, leafPath: leafPath}]
	if !ok && modulePrefix == "" {
		if _, ambiguous := r.ambiguous[leafPath]; !ambiguous {
			info, ok = r.byLeafPath[leafPath]
		}
	}
	if !ok {
		r.mu.RUnlock()
		return "", false
	}
	kind := info.Kind
	r.mu.RUnlock()
	r.cache.Set(modulePrefix, leafPath, kind)
	return kind, true
}

func (r *Registry) CacheSize() int {
	if r == nil || r.cache == nil {
		return 0
	}
	return r.cache.Size()
}

func (r *Registry) walkModule(root *goyang.Entry, moduleName, modulePrefix string) {
	if root == nil {
		return
	}
	r.walkEntry(root, nil, moduleName, modulePrefix)
}

func (r *Registry) walkEntry(entry *goyang.Entry, path []string, moduleName, modulePrefix string) {
	if entry == nil {
		return
	}
	if entry.Parent != nil {
		path = append(path, entry.Name)
	}
	if entry.IsLeaf() || entry.IsLeafList() {
		if kind, ok := classifyYangType(entry.Type); ok {
			leafPath := "/" + strings.Join(path, "/")
			info := TypeInfo{
				ModuleName:   moduleName,
				ModulePrefix: modulePrefix,
				LeafPath:     leafPath,
				TypeName:     yangTypeName(entry.Type),
				Kind:         kind,
			}
			r.addInfo(info)
		}
		return
	}
	if len(entry.Dir) == 0 {
		return
	}
	names := make([]string, 0, len(entry.Dir))
	for name := range entry.Dir {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		r.walkEntry(entry.Dir[name], append([]string(nil), path...), moduleName, modulePrefix)
	}
}

func (r *Registry) addInfo(info TypeInfo) {
	for _, module := range []string{info.ModuleName, info.ModulePrefix} {
		if module == "" {
			continue
		}
		r.byKey[lookupKey{modulePrefix: module, leafPath: info.LeafPath}] = info
	}
	existing, ok := r.byLeafPath[info.LeafPath]
	switch {
	case !ok:
		r.byLeafPath[info.LeafPath] = info
	case existing.Kind != info.Kind:
		delete(r.byLeafPath, info.LeafPath)
		r.ambiguous[info.LeafPath] = struct{}{}
	}
}

func classifyYangType(t *goyang.YangType) (classifier.MetricKind, bool) {
	if t == nil {
		return "", false
	}
	if hasTypeName(t, "counter32", "counter64") {
		return classifier.MetricKindSum, true
	}
	switch t.Kind {
	case goyang.Yint8, goyang.Yint16, goyang.Yint32, goyang.Yint64,
		goyang.Yuint8, goyang.Yuint16, goyang.Yuint32, goyang.Yuint64,
		goyang.Ydecimal64, goyang.Ystring, goyang.Yenum:
		return classifier.MetricKindGauge, true
	default:
		return "", false
	}
}

func hasTypeName(t *goyang.YangType, names ...string) bool {
	want := make(map[string]struct{}, len(names))
	for _, name := range names {
		want[name] = struct{}{}
	}
	seen := map[*goyang.YangType]struct{}{}
	var visit func(*goyang.YangType) bool
	visit = func(cur *goyang.YangType) bool {
		if cur == nil {
			return false
		}
		if _, ok := seen[cur]; ok {
			return false
		}
		seen[cur] = struct{}{}
		for _, name := range []string{cur.Name, typeName(cur.Base)} {
			if _, ok := want[stripTypePrefix(name)]; ok {
				return true
			}
		}
		if cur.Root != nil && cur.Root != cur && visit(cur.Root) {
			return true
		}
		if cur.Base != nil && cur.Base.YangType != nil && visit(cur.Base.YangType) {
			return true
		}
		return false
	}
	return visit(t)
}

func yangTypeName(t *goyang.YangType) string {
	if t == nil {
		return ""
	}
	if t.Name != "" {
		return t.Name
	}
	return t.Kind.String()
}

func typeName(t *goyang.Type) string {
	if t == nil {
		return ""
	}
	return t.Name
}

func stripTypePrefix(name string) string {
	if idx := strings.IndexByte(name, ':'); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func splitCanonicalPath(path string) (modulePrefix, leafPath string) {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return "", "/"
	}
	path = "/" + strings.Trim(path, "/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		if idx := strings.IndexByte(part, '['); idx >= 0 {
			part = part[:idx]
		}
		if i == 0 {
			if idx := strings.IndexByte(part, ':'); idx >= 0 {
				modulePrefix = part[:idx]
				part = part[idx+1:]
			}
		} else if idx := strings.IndexByte(part, ':'); idx >= 0 {
			part = part[idx+1:]
		}
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return modulePrefix, "/"
	}
	return modulePrefix, "/" + strings.Join(out, "/")
}
