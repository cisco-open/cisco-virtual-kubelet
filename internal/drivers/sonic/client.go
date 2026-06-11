// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	defaultSONICGNMIPort       = 57400
	defaultSONICRequestTimeout = 30 * time.Second
	defaultSONICSSHPort        = 22
	defaultSONICSSHConnect     = 15 * time.Second
	defaultSONICCommandTimeout = 45 * time.Second
)

type gnmiClient interface {
	Capabilities(ctx context.Context) (*gpb.CapabilityResponse, error)
	GetJSON(ctx context.Context, path string) ([]byte, error)
	Set(ctx context.Context, ops []OpenConfigOperation) error
	Close() error
}

type sonicGNMIClient struct {
	address  string
	port     int
	username string
	password string
	tls      bool
	insecure bool
	timeout  time.Duration

	mu   sync.Mutex
	conn *grpc.ClientConn
}

func newGNMIClientFromSpec(spec *v1alpha1.DeviceSpec, password string) (*sonicGNMIClient, error) {
	if spec == nil {
		return nil, fmt.Errorf("sonic gnmi: nil device spec")
	}
	if spec.Address == "" {
		return nil, fmt.Errorf("sonic gnmi: device address is required")
	}
	if password == "" {
		password = spec.Password
	}
	port := defaultSONICGNMIPort
	tlsEnabled := false
	insecureSkip := false
	timeout := defaultSONICRequestTimeout
	if spec.Port > 0 && spec.Port != 22 && spec.Port != 80 && spec.Port != 443 {
		port = spec.Port
	}
	if spec.SONIC != nil && spec.SONIC.OpenConfig != nil {
		oc := spec.SONIC.OpenConfig
		if oc.GNMIPort > 0 {
			port = oc.GNMIPort
		}
		tlsEnabled = oc.TLS
		insecureSkip = oc.InsecureSkipVerify
		if oc.RequestTimeoutSeconds > 0 {
			timeout = time.Duration(oc.RequestTimeoutSeconds) * time.Second
		}
	}
	return &sonicGNMIClient{
		address:  spec.Address,
		port:     port,
		username: spec.Username,
		password: password,
		tls:      tlsEnabled,
		insecure: insecureSkip,
		timeout:  timeout,
	}, nil
}

func (c *sonicGNMIClient) target() string {
	host := c.address
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return net.JoinHostPort(host, strconv.Itoa(c.port))
}

func (c *sonicGNMIClient) dial(ctx context.Context) (gpb.GNMIClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		return gpb.NewGNMIClient(c.conn), nil
	}
	dialCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var creds credentials.TransportCredentials
	if c.tls {
		creds = credentials.NewTLS(&tls.Config{InsecureSkipVerify: c.insecure}) // #nosec G402 - operator-controlled for lab images.
	} else {
		creds = insecure.NewCredentials()
	}
	conn, err := grpc.DialContext(dialCtx, c.target(), grpc.WithTransportCredentials(creds), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("sonic gnmi dial %s: %w", c.target(), err)
	}
	c.conn = conn
	return gpb.NewGNMIClient(conn), nil
}

func (c *sonicGNMIClient) authCtx(ctx context.Context) context.Context {
	if c.username == "" {
		return ctx
	}
	creds := base64.StdEncoding.EncodeToString([]byte(c.username + ":" + c.password))
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Basic "+creds)
}

func (c *sonicGNMIClient) rpcCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.authCtx(ctx), c.timeout)
}

func (c *sonicGNMIClient) Capabilities(ctx context.Context) (*gpb.CapabilityResponse, error) {
	cli, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	rpcCtx, cancel := c.rpcCtx(ctx)
	defer cancel()
	resp, err := cli.Capabilities(rpcCtx, &gpb.CapabilityRequest{})
	if err != nil {
		return nil, fmt.Errorf("sonic gnmi capabilities: %w", err)
	}
	return resp, nil
}

func (c *sonicGNMIClient) GetJSON(ctx context.Context, path string) ([]byte, error) {
	cli, err := c.dial(ctx)
	if err != nil {
		return nil, err
	}
	p, err := parseGNMIPath(path)
	if err != nil {
		return nil, err
	}
	rpcCtx, cancel := c.rpcCtx(ctx)
	defer cancel()
	resp, err := cli.Get(rpcCtx, &gpb.GetRequest{Path: []*gpb.Path{p}, Encoding: gpb.Encoding_JSON_IETF, Type: gpb.GetRequest_ALL})
	if err != nil {
		return nil, fmt.Errorf("sonic gnmi get %s: %w", path, err)
	}
	for _, n := range resp.GetNotification() {
		for _, u := range n.GetUpdate() {
			if tv := u.GetVal(); tv != nil {
				if b := tv.GetJsonIetfVal(); len(b) > 0 {
					return b, nil
				}
				if b := tv.GetJsonVal(); len(b) > 0 {
					return b, nil
				}
			}
		}
	}
	return nil, nil
}

func (c *sonicGNMIClient) Set(ctx context.Context, ops []OpenConfigOperation) error {
	if len(ops) == 0 {
		return nil
	}
	cli, err := c.dial(ctx)
	if err != nil {
		return err
	}
	req := &gpb.SetRequest{}
	for i, op := range ops {
		p, err := parseGNMIPath(op.Path)
		if err != nil {
			return fmt.Errorf("op[%d] path %q: %w", i, op.Path, err)
		}
		switch op.Verb {
		case OperationReplace:
			val, err := jsonValueForPath(op.Path, op.Value)
			if err != nil {
				return fmt.Errorf("op[%d] value: %w", i, err)
			}
			req.Replace = append(req.Replace, &gpb.Update{Path: p, Val: val})
		case OperationUpdate:
			val, err := jsonValueForPath(op.Path, op.Value)
			if err != nil {
				return fmt.Errorf("op[%d] value: %w", i, err)
			}
			req.Update = append(req.Update, &gpb.Update{Path: p, Val: val})
		case OperationDelete:
			req.Delete = append(req.Delete, p)
		default:
			return fmt.Errorf("op[%d]: unsupported operation %q", i, op.Verb)
		}
	}
	rpcCtx, cancel := c.rpcCtx(ctx)
	defer cancel()
	if _, err := cli.Set(rpcCtx, req); err != nil {
		if strings.Contains(err.Error(), "Translib write is disabled") {
			return fmt.Errorf("sonic gnmi set: translib write is disabled on target; enable SONiC telemetry with --gnmi_translib_write: %w", err)
		}
		return fmt.Errorf("sonic gnmi set: %w", err)
	}
	return nil
}

func jsonValueForPath(path string, raw json.RawMessage) (*gpb.TypedValue, error) {
	if len(raw) == 0 {
		raw = []byte("null")
	}
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: raw}}, nil
	}
	leaf := leafName(path)
	if leaf == "" {
		return nil, fmt.Errorf("scalar OpenConfig value requires a leaf path")
	}
	wrapped, err := json.Marshal(map[string]json.RawMessage{leaf: raw})
	if err != nil {
		return nil, err
	}
	return &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: wrapped}}, nil
}

func leafName(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return ""
	}
	parts := strings.Split(path, "/")
	leaf := parts[len(parts)-1]
	if idx := strings.Index(leaf, "["); idx >= 0 {
		leaf = leaf[:idx]
	}
	if idx := strings.Index(leaf, "="); idx >= 0 {
		leaf = leaf[:idx]
	}
	return stripModule(leaf)
}

func (c *sonicGNMIClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func parseGNMIPath(path string) (*gpb.Path, error) {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return &gpb.Path{}, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, fmt.Errorf("path must start with '/'")
	}
	out := &gpb.Path{}
	for _, raw := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if raw == "" {
			continue
		}
		name, keys, err := parsePathElem(raw)
		if err != nil {
			return nil, err
		}
		out.Elem = append(out.Elem, &gpb.PathElem{Name: stripModule(name), Key: keys})
	}
	return out, nil
}

func parsePathElem(raw string) (string, map[string]string, error) {
	name := raw
	keys := map[string]string{}
	if idx := strings.Index(raw, "["); idx >= 0 {
		name = raw[:idx]
		rest := raw[idx:]
		for rest != "" {
			if !strings.HasPrefix(rest, "[") {
				return "", nil, fmt.Errorf("malformed path segment %q", raw)
			}
			end := strings.Index(rest, "]")
			if end < 0 {
				return "", nil, fmt.Errorf("unterminated key in path segment %q", raw)
			}
			kv := rest[1:end]
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) != 2 || parts[0] == "" {
				return "", nil, fmt.Errorf("malformed key %q in path segment %q", kv, raw)
			}
			keys[stripModule(parts[0])] = strings.Trim(parts[1], "'\"")
			rest = rest[end+1:]
		}
	}
	if i := strings.Index(name, "="); i > 0 {
		// Backward-compatible shorthand: /interface=Ethernet0 means [name=Ethernet0].
		keys["name"] = strings.Trim(name[i+1:], "'\"")
		name = name[:i]
	}
	if len(keys) == 0 {
		keys = nil
	}
	if name == "" {
		return "", nil, fmt.Errorf("empty path segment %q", raw)
	}
	return name, keys, nil
}

func stripModule(s string) string {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[i+1:]
	}
	return s
}
