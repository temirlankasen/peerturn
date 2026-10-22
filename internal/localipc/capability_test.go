package localipc

import (
	"context"
	"testing"
)

func TestCapabilityLeasesLeaveCapacityForOwnerRequests(t *testing.T) {
	dir := runtimeFixture(t)
	server, err := Listen(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, func(_ context.Context, request Request) (any, error) {
			return map[string]string{"method": request.Method}, nil
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	client := Client{RuntimeDir: dir}
	for range 64 {
		_, release, err := client.OpenCapability(t.Context(), "explicit")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := release(); err != nil {
				t.Error(err)
			}
		})
	}
	if _, err := client.Call(t.Context(), Request{Method: "status"}); err != nil {
		t.Fatalf("idle MCP leases block trusted owner controls: %v", err)
	}
}
