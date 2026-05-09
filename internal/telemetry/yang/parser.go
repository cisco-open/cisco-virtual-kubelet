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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyang "github.com/openconfig/goyang/pkg/yang"
)

const ModelsDirEnv = "YANG_MODELS_DIR"

// Parser loads RFC 6020/7950 YANG modules and compiles resolved leaf types
// into a registry suitable for telemetry metric classification.
type Parser struct {
	modules *goyang.Modules
}

func NewParser(searchPaths ...string) *Parser {
	modules := goyang.NewModules()
	for _, path := range searchPaths {
		if strings.TrimSpace(path) != "" {
			modules.AddPath(path)
		}
	}
	return &Parser{modules: modules}
}

func NewRegistryFromEnv() (*Registry, error) {
	dir := strings.TrimSpace(os.Getenv(ModelsDirEnv))
	if dir == "" {
		return nil, nil
	}
	return NewRegistryFromDir(dir)
}

func NewRegistryFromDir(dir string) (*Registry, error) {
	parser := NewParser(dir)
	if err := parser.LoadDir(dir); err != nil {
		return nil, err
	}
	return parser.Registry()
}

func (p *Parser) LoadDir(dir string) error {
	if p == nil || p.modules == nil {
		return errors.New("nil YANG parser")
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return fmt.Errorf("%s is empty", ModelsDirEnv)
	}
	var files []string
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d == nil || d.IsDir() || filepath.Ext(path) != ".yang" {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return fmt.Errorf("walk YANG models dir %q: %w", dir, err)
	}
	sort.Strings(files)
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("read YANG module %q: %w", file, err)
		}
		if err := p.ParseModuleContent(string(data), file); err != nil {
			return err
		}
	}
	return nil
}

func (p *Parser) ParseModuleContent(content, filename string) error {
	if p == nil || p.modules == nil {
		return errors.New("nil YANG parser")
	}
	if strings.TrimSpace(filename) == "" {
		filename = "inline.yang"
	}
	if err := p.modules.Parse(content, filename); err != nil {
		return fmt.Errorf("parse YANG module %q: %w", filename, err)
	}
	return nil
}

func (p *Parser) Registry() (*Registry, error) {
	if p == nil || p.modules == nil {
		return nil, errors.New("nil YANG parser")
	}
	if errs := p.modules.Process(); len(errs) > 0 {
		return nil, fmt.Errorf("process YANG modules: %w", errors.Join(errs...))
	}
	return newRegistryFromModules(p.modules), nil
}
