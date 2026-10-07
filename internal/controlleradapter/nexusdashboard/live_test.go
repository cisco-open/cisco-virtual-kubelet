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

//go:build live

package nexusdashboard

// Opt-in tests against a real Nexus Dashboard. They are excluded from normal
// runs by the "live" build tag and need no Kubernetes cluster:
//
//	ND_ENDPOINT=https://nd.lab ND_USERNAME=cvk-reader ND_PASSWORD=... \
//	ND_INSECURE=true go test -tags live -run TestLive -v \
//	./internal/controlleradapter/nexusdashboard/
//
// Optional: ND_DOMAIN (default local), ND_CA_FILE (PEM bundle instead of
// ND_INSECURE), and ND_DUMP_PATHS (comma-separated GET paths such as
// /api/v1/manage/fabrics) with ND_DUMP_DIR (default: a temp dir) to save raw
// responses. Dumped files are unsanitized: review them before sharing.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func liveClient(t *testing.T, password string) *client {
	t.Helper()
	endpoint, user := os.Getenv("ND_ENDPOINT"), os.Getenv("ND_USERNAME")
	if endpoint == "" || user == "" || os.Getenv("ND_PASSWORD") == "" {
		t.Skip("set ND_ENDPOINT, ND_USERNAME and ND_PASSWORD to run live tests")
	}
	dir := t.TempDir()
	files := map[string]string{"username": user, "password": password}
	if d := os.Getenv("ND_DOMAIN"); d != "" {
		files["domain"] = d
	}
	for name, v := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return newClient(clientConfig{
		Endpoint:           endpoint,
		CredentialPath:     dir,
		CAPath:             os.Getenv("ND_CA_FILE"),
		InsecureSkipVerify: os.Getenv("ND_INSECURE") == "true",
		RequestTimeout:     30 * time.Second,
		MaxSessionLifetime: 15 * time.Minute,
		MaxConcurrent:      2,
	})
}

func TestLiveHealthy(t *testing.T) {
	c := liveClient(t, os.Getenv("ND_PASSWORD"))
	res := classify(c.Probe(context.Background()))
	t.Logf("result: phase=%s reason=%s message=%q", res.phase, res.reason, res.message)
	if !res.ready() {
		t.Fatalf("expected Ready, got %+v", res)
	}
}

func TestLiveBadPassword(t *testing.T) {
	good := os.Getenv("ND_PASSWORD")
	c := liveClient(t, good+"-wrong")
	res := classify(c.Probe(context.Background()))
	t.Logf("result: phase=%s reason=%s message=%q", res.phase, res.reason, res.message)
	if res.authenticated || res.phase != "Error" {
		t.Fatalf("expected Authenticated=False/Error, got %+v", res)
	}
	if strings.Contains(res.message, good) || strings.Contains(res.message, good+"-wrong") {
		t.Fatal("password leaked into status message")
	}
}

func TestLiveDumpPaths(t *testing.T) {
	paths := os.Getenv("ND_DUMP_PATHS")
	if paths == "" {
		t.Skip("set ND_DUMP_PATHS to dump raw GET responses")
	}
	c := liveClient(t, os.Getenv("ND_PASSWORD"))
	out := os.Getenv("ND_DUMP_DIR")
	if out == "" {
		out = t.TempDir()
	}
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range strings.Split(paths, ",") {
		p = strings.TrimSpace(p)
		body, err := c.Get(context.Background(), p, nil)
		if err != nil {
			t.Errorf("GET %s: %v", p, err)
			continue
		}
		name := strings.Trim(strings.ReplaceAll(p, "/", "_"), "_") + ".json"
		if err := os.WriteFile(filepath.Join(out, name), body, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Logf("saved %d bytes of GET %s to %s", len(body), p, filepath.Join(out, name))
	}
}
