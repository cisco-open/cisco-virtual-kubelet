// Copyright © 2022 Cisco Systems, Inc. and its affiliates.
// All rights reserved.
//
// Licensed under the Mozilla Public License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://mozilla.org/MPL/2.0/
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: MPL-2.0

package nacmodel

import (
	"reflect"
	"testing"
)

func TestModelMergeUpstreamMaps(t *testing.T) {
	cases := []struct {
		dst    map[string]any
		src    map[string]any
		result map[string]any
	}{
		// merge maps
		{
			dst: map[string]any{
				"e1": "abc",
			},
			src: map[string]any{
				"e2": "def",
			},
			result: map[string]any{
				"e1": "abc",
				"e2": "def",
			},
		},
		// merge empty destination map
		{
			dst: map[string]any{
				"e1": nil,
			},
			src: map[string]any{
				"e1": "abc",
			},
			result: map[string]any{
				"e1": "abc",
			},
		},
		// merge empty destination map nested
		{
			dst: map[string]any{
				"e1": nil,
			},
			src: map[string]any{
				"e1": map[string]any{
					"e2": "abc",
				},
			},
			result: map[string]any{
				"e1": map[string]any{
					"e2": "abc",
				},
			},
		},
		// merge empty source map
		{
			dst: map[string]any{
				"e1": "abc",
			},
			src: map[string]any{
				"e1": nil,
			},
			result: map[string]any{
				"e1": "abc",
			},
		},
		// merge empty source map nested
		{
			dst: map[string]any{
				"e1": map[string]any{
					"e2": "abc",
				},
			},
			src: map[string]any{
				"e1": nil,
			},
			result: map[string]any{
				"e1": map[string]any{
					"e2": "abc",
				},
			},
		},
		// merge nested maps
		{
			dst: map[string]any{
				"root": map[string]any{
					"child1": "abc",
				},
			},
			src: map[string]any{
				"root": map[string]any{
					"child2": "def",
				},
			},
			result: map[string]any{
				"root": map[string]any{
					"child1": "abc",
					"child2": "def",
				},
			},
		},
		// append when merging lists
		{
			dst: map[string]any{
				"list": []any{
					map[string]any{
						"child1": "abc",
					},
				},
			},
			src: map[string]any{
				"list": []any{
					map[string]any{
						"child2": "def",
					},
				},
			},
			result: map[string]any{
				"list": []any{
					map[string]any{
						"child1": "abc",
					},
					map[string]any{
						"child2": "def",
					},
				},
			},
		},
		// merge matching items across lists (no duplicates within each list)
		{
			dst: map[string]any{
				"list": []any{
					map[string]any{
						"child1": "abc",
					},
				},
			},
			src: map[string]any{
				"list": []any{
					map[string]any{
						"child1": "abc",
					},
				},
			},
			result: map[string]any{
				"list": []any{
					map[string]any{
						"child1": "abc",
					},
				},
			},
		},
		// src bool replaces dst primitive value
		{
			dst: map[string]any{
				"attr": false,
			},
			src: map[string]any{
				"attr": true,
			},
			result: map[string]any{
				"attr": true,
			},
		},
		{
			dst: map[string]any{
				"attr": true,
			},
			src: map[string]any{
				"attr": false,
			},
			result: map[string]any{
				"attr": false,
			},
		},
		// empty src string replaces dst string
		{
			dst: map[string]any{
				"attr": "abc",
			},
			src: map[string]any{
				"attr": "",
			},
			result: map[string]any{
				"attr": "",
			},
		},
		// src string replaces dst string
		{
			dst: map[string]any{
				"attr": "abc",
			},
			src: map[string]any{
				"attr": "def",
			},
			result: map[string]any{
				"attr": "def",
			},
		},
		// src number does replace dst number
		{
			dst: map[string]any{
				"attr": 5,
			},
			src: map[string]any{
				"attr": 0,
			},
			result: map[string]any{
				"attr": 0,
			},
		},
		// src number does replace dst string
		{
			dst: map[string]any{
				"attr": "abc",
			},
			src: map[string]any{
				"attr": 0,
			},
			result: map[string]any{
				"attr": 0,
			},
		},
		// src string does replace dst number
		{
			dst: map[string]any{
				"attr": 5,
			},
			src: map[string]any{
				"attr": "abc",
			},
			result: map[string]any{
				"attr": "abc",
			},
		},
		// empty src map does not replace dst map
		{
			dst: map[string]any{
				"attr": "abc",
			},
			src: map[string]any{},
			result: map[string]any{
				"attr": "abc",
			},
		},
		// src map gets merged with dst map
		{
			dst: map[string]any{},
			src: map[string]any{
				"attr": "abc",
			},
			result: map[string]any{
				"attr": "abc",
			},
		},
		// concatenate when source list has duplicates (preserve duplicates)
		{
			dst: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
						"x":    1,
					},
				},
			},
			src: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
					},
					map[string]any{
						"name": "a",
					},
				},
			},
			result: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
						"x":    1,
					},
					map[string]any{
						"name": "a",
					},
					map[string]any{
						"name": "a",
					},
				},
			},
		},
		// concatenate when destination list has duplicates (preserve duplicates)
		{
			dst: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
					},
					map[string]any{
						"name": "a",
					},
				},
			},
			src: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
						"x":    1,
					},
				},
			},
			result: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
					},
					map[string]any{
						"name": "a",
					},
					map[string]any{
						"name": "a",
						"x":    1,
					},
				},
			},
		},
		// merge when no duplicates present
		{
			dst: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
						"x":    1,
					},
				},
			},
			src: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
						"y":    2,
					},
				},
			},
			result: map[string]any{
				"list": []any{
					map[string]any{
						"name": "a",
						"x":    1,
						"y":    2,
					},
				},
			},
		},
	}

	for _, c := range cases {
		mergeModelMaps(c.src, c.dst, true)
		if !reflect.DeepEqual(c.dst, c.result) {
			t.Fatalf("Error matching dst and result: %#v vs %#v", c.dst, c.result)
		}
	}
}
