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

package transport

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	gpb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakeGNMIServer captures the SetRequest the transport sends and
// emits a canned GetResponse so Fetch round-trips are verifiable.
// Only the methods exercised by the transport are implemented;
// Capabilities and Subscribe deliberately remain stubs.
type fakeGNMIServer struct {
	gpb.UnimplementedGNMIServer
	mu                     sync.Mutex
	lastSet                *gpb.SetRequest
	getResponse            []byte
	subscribeNotifications []*gpb.Notification
}

func (s *fakeGNMIServer) Set(ctx context.Context, req *gpb.SetRequest) (*gpb.SetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastSet = req
	return &gpb.SetResponse{}, nil
}

// Subscribe is a streaming RPC; we simulate the stream by pushing
// canned notifications onto the server-side stream and closing it.
// The fixture is small on purpose — it covers the happy path and
// the close path; richer behaviour belongs in dedicated tests if
// that surface grows.
func (s *fakeGNMIServer) Subscribe(stream gpb.GNMI_SubscribeServer) error {
	// Read the first SubscribeRequest so we know the client is
	// connected, then ignore its content — the test fixture
	// drives the response side directly.
	if _, err := stream.Recv(); err != nil {
		return err
	}
	for _, n := range s.subscribeNotifications {
		if err := stream.Send(&gpb.SubscribeResponse{
			Response: &gpb.SubscribeResponse_Update{Update: n},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeGNMIServer) Get(ctx context.Context, req *gpb.GetRequest) (*gpb.GetResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &gpb.GetResponse{
		Notification: []*gpb.Notification{{
			Update: []*gpb.Update{{
				Path: req.Path[0],
				Val:  &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: s.getResponse}},
			}},
		}},
	}, nil
}

func newFakeGNMI(t *testing.T) (*fakeGNMIServer, *grpc.ClientConn) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	fake := &fakeGNMIServer{}
	gpb.RegisterGNMIServer(srv, fake)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return fake, conn
}

func TestGNMIFetchUnpacksJSONIETFValue(t *testing.T) {
	fake, conn := newFakeGNMI(t)
	fake.getResponse = []byte(`{"vlan-list":[{"id":10}]}`)
	cli, err := NewGNMI(GNMIConfig{Conn: conn})
	if err != nil {
		t.Fatalf("NewGNMI: %v", err)
	}
	body, err := cli.Fetch(context.Background(), "/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if string(body) != string(fake.getResponse) {
		t.Errorf("body=%s, want %s", body, fake.getResponse)
	}
}

func TestGNMIMutateBatchesAsSingleSetRequest(t *testing.T) {
	// Replace + Merge + Delete in one Mutate call must produce one
	// SetRequest at the gNMI wire — gNMI Set is atomic per-request,
	// which is how transactional transports come to the table.
	fake, conn := newFakeGNMI(t)
	cli, _ := NewGNMI(GNMIConfig{Conn: conn})

	ops := []Op{
		{Verb: VerbReplace, Path: "/a", Body: []byte(`{}`)},
		{Verb: VerbMerge, Path: "/b", Body: []byte(`{}`)},
		{Verb: VerbDelete, Path: "/c"},
	}
	if err := cli.Mutate(context.Background(), "", ops); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	got := fake.lastSet
	if got == nil {
		t.Fatal("server saw no SetRequest")
	}
	if len(got.Replace) != 1 || len(got.Update) != 1 || len(got.Delete) != 1 {
		t.Errorf("op classification: replace=%d update=%d delete=%d, want 1/1/1",
			len(got.Replace), len(got.Update), len(got.Delete))
	}
}

func TestGNMITransactionalCommitFlushesQueuedOps(t *testing.T) {
	fake, conn := newFakeGNMI(t)
	cli, _ := NewGNMI(GNMIConfig{Conn: conn})

	tx, err := cli.StartTransaction(context.Background())
	if err != nil {
		t.Fatalf("StartTransaction: %v", err)
	}
	if tx == "" {
		t.Fatal("expected non-empty TxHandle")
	}
	if err := cli.Mutate(context.Background(), tx, []Op{
		{Verb: VerbMerge, Path: "/a", Body: []byte(`{}`)},
	}); err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	// Nothing should have hit the wire yet — that's the whole
	// point of transactional batching.
	if fake.lastSet != nil {
		t.Fatalf("queued op leaked to wire before Commit: %+v", fake.lastSet)
	}
	if err := cli.Commit(context.Background(), tx); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if fake.lastSet == nil || len(fake.lastSet.Update) != 1 {
		t.Fatalf("Commit did not flush exactly one update: %+v", fake.lastSet)
	}
}

func TestGNMITransactionalDiscardDropsQueue(t *testing.T) {
	fake, conn := newFakeGNMI(t)
	cli, _ := NewGNMI(GNMIConfig{Conn: conn})
	tx, _ := cli.StartTransaction(context.Background())
	_ = cli.Mutate(context.Background(), tx, []Op{
		{Verb: VerbMerge, Path: "/a", Body: []byte(`{}`)},
	})
	if err := cli.Discard(context.Background(), tx); err != nil {
		t.Fatalf("Discard: %v", err)
	}
	if fake.lastSet != nil {
		t.Errorf("Discard let queued ops escape: %+v", fake.lastSet)
	}
	// A fresh Start after Discard must succeed — Discard releases
	// the transaction slot.
	if _, err := cli.StartTransaction(context.Background()); err != nil {
		t.Fatalf("Start after Discard: %v", err)
	}
}

func TestGNMICapabilities(t *testing.T) {
	cli, _ := NewGNMI(GNMIConfig{Address: "x"})
	c := cli.Capabilities()
	if c.Kind != KindGNMI {
		t.Errorf("Kind=%v", c.Kind)
	}
	if !c.SupportsTransactions {
		t.Error("gNMI advertises atomic Set; SupportsTransactions should be true")
	}
	if c.SupportsSaveStartup {
		t.Error("gNMI has no save-config primitive; should not advertise it")
	}
}

func TestGNMISaveStartupReportsUnsupported(t *testing.T) {
	cli, _ := NewGNMI(GNMIConfig{Address: "x"})
	err := cli.SaveStartup(context.Background())
	if err == nil {
		t.Fatal("expected ErrUnsupported")
	}
}

func TestGNMICLIVerbRejected(t *testing.T) {
	// CLI ops are pushed via the Cisco-IA RPC, which is a NETCONF/
	// RESTCONF construct — gNMI doesn't carry it. We must reject
	// CLI ops loudly rather than silently no-op so a misrouted CLI
	// template doesn't disappear.
	_, conn := newFakeGNMI(t)
	cli, _ := NewGNMI(GNMIConfig{Conn: conn})
	err := cli.Mutate(context.Background(), "", []Op{
		{Verb: VerbCLI, Body: []byte("hostname x")},
	})
	if err == nil {
		t.Fatal("expected error for CLI verb on gNMI transport")
	}
}

func TestGNMISubscribeFlattensNotificationsToEvents(t *testing.T) {
	// Two updates and one delete in one notification must surface
	// as three SubscribeEvent values in stream order. JSON_IETF
	// payloads round-trip verbatim.
	fake, conn := newFakeGNMI(t)
	fake.subscribeNotifications = []*gpb.Notification{{
		Update: []*gpb.Update{
			{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "vlan-list"}}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"id":10}`)}}},
			{Path: &gpb.Path{Elem: []*gpb.PathElem{{Name: "vlan-list"}}},
				Val: &gpb.TypedValue{Value: &gpb.TypedValue_JsonIetfVal{JsonIetfVal: []byte(`{"id":20}`)}}},
		},
		Delete: []*gpb.Path{{Elem: []*gpb.PathElem{{Name: "vlan-list"}}}},
	}}
	cli, _ := NewGNMI(GNMIConfig{Conn: conn})
	subscriber, ok := cli.(SubscribeCapable)
	if !ok {
		t.Fatal("gnmi transport does not advertise SubscribeCapable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := subscriber.Subscribe(ctx, []string{"/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list"}, SubscribeOnChange)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	got := []SubscribeEvent{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				goto done
			}
			got = append(got, ev)
		case <-deadline:
			goto done
		}
	}
done:
	// Three real events plus one terminal Err event when the
	// server closes the stream.
	if len(got) < 3 {
		t.Fatalf("got %d events, want at least 3: %#v", len(got), got)
	}
	if got[0].Delete || got[1].Delete {
		t.Errorf("update events misclassified as delete: %#v", got[:2])
	}
	if !got[2].Delete {
		t.Errorf("delete event not flagged: %#v", got[2])
	}
}

func TestGNMIParsePathConvertsRESTCONFShape(t *testing.T) {
	// Round-trip the conversion so a regression here is loud.
	p, err := parseGNMIPath("/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list=10")
	if err != nil {
		t.Fatalf("parseGNMIPath: %v", err)
	}
	want := []string{"native", "vlan", "vlan-list"}
	if len(p.Elem) != len(want) {
		t.Fatalf("got %d elems, want %d: %#v", len(p.Elem), len(want), p.Elem)
	}
	for i, name := range want {
		if p.Elem[i].Name != name {
			t.Errorf("elem[%d].Name=%q, want %q", i, p.Elem[i].Name, name)
		}
	}
	last := p.Elem[len(p.Elem)-1]
	if last.Key["id"] != "10" {
		t.Errorf("numeric key not extracted: %#v", last.Key)
	}
}
