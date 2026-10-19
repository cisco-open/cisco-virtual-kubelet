// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// FRA requests are counted independently; all other requests go through the
// existing CleanAir guard, which refuses unrelated mutations. No bodies or
// credentials are logged by either guard.
type fraManifestTrace struct {
	base                                    http.RoundTripper
	fallback                                *cleanAirManifestTrace
	name, id                                string
	maxPuts, posts, puts, deletes, attempts int
	allowDelete                             bool
}

func (g *fraManifestTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodGet || req.URL.Path != wirelessFRAPath && !strings.HasPrefix(req.URL.Path, wirelessFRAPath+"/") {
		return g.fallback.RoundTrip(req)
	}
	g.attempts++
	if req.Method == http.MethodDelete {
		if !g.allowDelete || g.id == "" || g.deletes != 0 || req.URL.Path != wirelessFRAPath+"/"+url.PathEscape(g.id) {
			return nil, errors.New("qualification refused unrelated or repeated FRA deletion")
		}
		g.deletes++
	} else {
		raw, err := io.ReadAll(io.LimitReader(req.Body, 65536))
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(raw))
		var body wirelessFRA
		if json.Unmarshal(raw, &body) != nil || validateWirelessFRA(body) != nil || body.Name != g.name || body.ID != "" {
			return nil, errors.New("qualification refused unrelated FRA payload")
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == wirelessFRAPath && g.posts == 0 && g.id == "":
			g.posts++
		case req.Method == http.MethodPut && g.id != "" && req.URL.Path == wirelessFRAPath+"/"+url.PathEscape(g.id) && g.puts < g.maxPuts:
			g.puts++
		default:
			return nil, errors.New("qualification refused unrelated or repeated FRA mutation")
		}
	}
	return g.base.RoundTrip(req)
}

func cleanupLiveFRAFeature(t *testing.T, ctx context.Context, c *client, trace *fraManifestTrace, baseline map[string]wirelessFeatureInstance, last wirelessFRA, save func(string, any), probePuts int) {
	t.Helper()
	current, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil {
		t.Fatal(err)
	}
	instance, exists := current[trace.name]
	if !exists || instance.ID != trace.id || *instance.System {
		t.Fatal("FRA cleanup identity changed", trace.name)
	}
	detail, err := c.wirelessFRARead(ctx, instance)
	if err != nil || !reflect.DeepEqual(detail, last) {
		t.Fatal("FRA cleanup fields changed", trace.name, err)
	}
	delete(current, trace.name)
	if !reflect.DeepEqual(current, baseline) {
		t.Fatal("FRA cleanup baseline changed", trace.name)
	}
	trace.allowDelete = true
	started := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessFRAPath+"/"+url.PathEscape(trace.id), nil)
	if err != nil {
		t.Fatal("FRA cleanup submission uncertain", trace.name, trace.id, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("FRA cleanup receipt uncertain", trace.name)
	}
	save("fra-cleanup-task.json", receipt.Response)
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, started); err != nil {
		t.Fatal("FRA cleanup task incomplete", trace.name, err)
	}
	after, err := c.wirelessFeatureInstances(ctx, wirelessFRAKind)
	if err != nil || !reflect.DeepEqual(after, baseline) {
		t.Fatal("FRA baseline not restored", trace.name, err)
	}
	if trace.posts != 1 || trace.puts != 6+probePuts || trace.deletes != 1 || trace.attempts != 8+probePuts {
		t.Fatal("FRA mutation count mismatch", trace.posts, trace.puts, trace.deletes, trace.attempts)
	}
	t.Log("exact generated FRA UUID cleanup and baseline restoration", trace.id, receipt.Response.ID)
}
