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

package controlleradapter

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestFoundationImportBoundary protects the dependency direction of the
// controller scaffold. Product adapters depend inward on this package; the
// foundation must not acquire device-driver/config-engine dependencies or
// reach outward into a concrete controller adapter.
func TestFoundationImportBoundary(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Clean(entry.Name())
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", path, err)
			}
			for _, forbidden := range []string{
				"/internal/drivers",
				"/internal/configengine",
				"/internal/platforms",
			} {
				if strings.Contains(importPath, forbidden) {
					t.Errorf("controller foundation %s imports forbidden dependency %q", path, importPath)
				}
			}
			if strings.Contains(importPath, "/internal/controlleradapter/") {
				t.Errorf("controller foundation %s imports concrete adapter %q", path, importPath)
			}
		}
	}
}
