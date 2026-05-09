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
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/classifier"
)

func TestParserResolvesCounter64Typedef(t *testing.T) {
	parser := NewParser()
	mustParse(t, parser, "ietf-yang-types.yang", `
module ietf-yang-types {
  namespace "urn:ietf:params:xml:ns:yang:ietf-yang-types";
  prefix yang;

  typedef counter64 {
    type uint64;
  }
}
`)
	mustParse(t, parser, "counters.yang", `
module counters {
  namespace "urn:test:counters";
  prefix ctr;

  import ietf-yang-types {
    prefix yang;
  }

  typedef counter64-with-units {
    type yang:counter64;
    units packets;
  }

  container stats {
    leaf packets {
      type counter64-with-units;
    }
  }
}
`)

	reg, err := parser.Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	got, ok := reg.Lookup("/counters:stats/packets")
	if !ok {
		t.Fatal("Lookup did not resolve packets leaf")
	}
	if got != classifier.MetricKindSum {
		t.Fatalf("kind=%s, want %s", got, classifier.MetricKindSum)
	}
}

func TestParserResolvesGaugeFromImport(t *testing.T) {
	parser := NewParser()
	mustParse(t, parser, "common.yang", `
module common {
  namespace "urn:test:common";
  prefix common;

  grouping interface-state {
    container state {
      leaf oper-status {
        type enumeration {
          enum up;
          enum down;
        }
      }
    }
  }
}
`)
	mustParse(t, parser, "interfaces.yang", `
module interfaces {
  namespace "urn:test:interfaces";
  prefix if;

  import common {
    prefix common;
  }

  container interfaces {
    list interface {
      key "name";
      leaf name {
        type string;
      }
      uses common:interface-state;
    }
  }
}
`)

	reg, err := parser.Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	got, ok := reg.Lookup("/interfaces/interface[name=Gi1]/state/oper-status")
	if !ok {
		t.Fatal("Lookup did not resolve imported grouping leaf")
	}
	if got != classifier.MetricKindGauge {
		t.Fatalf("kind=%s, want %s", got, classifier.MetricKindGauge)
	}
}

func TestUnknownPathReturnsUnknown(t *testing.T) {
	parser := NewParser()
	mustParse(t, parser, "empty.yang", `
module empty {
  namespace "urn:test:empty";
  prefix empty;
}
`)
	reg, err := parser.Registry()
	if err != nil {
		t.Fatalf("Registry: %v", err)
	}
	c := NewClassifier(reg, classifier.CuratedClassifier())
	got := c.Classify("/no/such/path")
	if got != classifier.MetricKindGauge {
		t.Fatalf("kind=%s, want fallback gauge", got)
	}
}

func TestCacheCapBasedReset(t *testing.T) {
	cache := NewCache(2)
	cache.Set("a", "/one", classifier.MetricKindGauge)
	cache.Set("a", "/two", classifier.MetricKindSum)
	if got := cache.Size(); got != 2 {
		t.Fatalf("size=%d, want 2", got)
	}
	cache.Set("a", "/three", classifier.MetricKindGauge)
	if got := cache.Size(); got != 1 {
		t.Fatalf("size=%d, want 1 after cap reset", got)
	}
	if _, ok := cache.Get("a", "/one"); ok {
		t.Fatal("old cache entry survived cap reset")
	}
	if got, ok := cache.Get("a", "/three"); !ok || got != classifier.MetricKindGauge {
		t.Fatalf("new cache entry=(%s,%t), want gauge,true", got, ok)
	}
}

func mustParse(t *testing.T, parser *Parser, filename, content string) {
	t.Helper()
	if err := parser.ParseModuleContent(content, filename); err != nil {
		t.Fatalf("ParseModuleContent(%s): %v", filename, err)
	}
}
