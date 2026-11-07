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

package nexusdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// aboutPath returns the ND product and build version. The response also
// carries the pre-login banner, which is deliberately never decoded.
const aboutPath = "/api/v1/infra/about"

// Version returns the ND product version, for example "4.3.1". It falls back
// to the build version when ND omits the product version.
func (c *client) Version(ctx context.Context) (string, error) {
	body, err := c.Get(ctx, aboutPath, nil)
	if err != nil {
		return "", err
	}
	var about struct {
		ProductVersion string `json:"productVersion"`
		BuildVersion   string `json:"buildVersion"`
	}
	if err := json.Unmarshal(body, &about); err != nil {
		return "", errors.New("version response is not valid JSON")
	}
	v := strings.TrimSpace(about.ProductVersion)
	if v == "" {
		v = strings.TrimSpace(about.BuildVersion)
	}
	if v == "" {
		return "", errors.New("version response has no product or build version")
	}
	return bound(v), nil
}
