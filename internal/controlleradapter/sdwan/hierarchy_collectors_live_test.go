package sdwan

import (
	"context"
	"os"
	"testing"
)

func TestLiveHierarchyCollectorProbe(t *testing.T) {
	if os.Getenv("CVK_SDWAN_COLLECTOR_PROBE") != "1" {
		t.Skip("explicit live probe")
	}
	c := liveClient(t)
	for _, kind := range []string{"cflowd", "security-logging"} {
		r := Resource{Kind: "hierarchy-setting", Type: kind}
		o, e := c.observeHierarchySetting(context.Background(), r)
		if e != nil {
			t.Fatal(kind, e)
		}
		t.Logf("%s existing=%t", kind, o != nil)
	}
}

func TestLiveHierarchyCollectorLifecycle(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/network-hierarchy-collectors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	liveParcelLifecycle(t, rs)
}
