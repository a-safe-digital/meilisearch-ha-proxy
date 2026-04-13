package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestNewNodeStartsHealthy(t *testing.T) {
	n := NewNode("http://localhost:7700", "", "primary", 3, 2)
	if n.State() != Healthy {
		t.Errorf("expected Healthy, got %s", n.State())
	}
	if !n.IsHealthy() {
		t.Error("expected IsHealthy() to be true")
	}
}

func TestStateTransitions_HealthyToSuspectOnFirstFailure(t *testing.T) {
	n := NewNode("http://localhost:7700", "", "primary", 3, 2)
	n.recordFailure(errTest)
	if n.State() != Suspect {
		t.Errorf("expected Suspect after 1 failure, got %s", n.State())
	}
}

func TestStateTransitions_SuspectToUnhealthyAfterThreshold(t *testing.T) {
	n := NewNode("http://localhost:7700", "", "primary", 3, 2)
	// 3 failures to reach unhealthy (threshold=3)
	for i := 0; i < 3; i++ {
		n.recordFailure(errTest)
	}
	if n.State() != Unhealthy {
		t.Errorf("expected Unhealthy after 3 failures, got %s", n.State())
	}
}

func TestStateTransitions_SuspectRecoveryOnSuccess(t *testing.T) {
	n := NewNode("http://localhost:7700", "", "primary", 3, 2)
	n.recordFailure(errTest) // Healthy -> Suspect
	n.recordSuccess()        // Suspect -> Healthy
	if n.State() != Healthy {
		t.Errorf("expected Healthy after recovery, got %s", n.State())
	}
}

func TestStateTransitions_UnhealthyNeedsMultipleSuccesses(t *testing.T) {
	n := NewNode("http://localhost:7700", "", "primary", 3, 2)
	// Drive to Unhealthy
	for i := 0; i < 3; i++ {
		n.recordFailure(errTest)
	}
	if n.State() != Unhealthy {
		t.Fatalf("expected Unhealthy, got %s", n.State())
	}

	// First success: Unhealthy -> Suspect
	n.recordSuccess()
	if n.State() != Suspect {
		t.Errorf("expected Suspect after 1 success, got %s", n.State())
	}

	// Second success: Suspect -> Healthy (healthy_threshold=2, but Suspect
	// transitions to Healthy on any success)
	n.recordSuccess()
	if n.State() != Healthy {
		t.Errorf("expected Healthy after 2 successes, got %s", n.State())
	}
}

func TestStateTransitions_UnhealthyStaysOnFailure(t *testing.T) {
	n := NewNode("http://localhost:7700", "", "primary", 3, 2)
	for i := 0; i < 5; i++ {
		n.recordFailure(errTest)
	}
	if n.State() != Unhealthy {
		t.Errorf("expected Unhealthy, got %s", n.State())
	}
}

func TestCheckHealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"available"}`))
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "test-key", "primary", 3, 2)
	n.Check(context.Background(), 2*time.Second)

	if n.State() != Healthy {
		t.Errorf("expected Healthy, got %s", n.State())
	}
	if n.LastError() != nil {
		t.Errorf("expected no error, got %v", n.LastError())
	}
}

func TestCheckUnhealthyServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "", "replica", 3, 2)
	n.Check(context.Background(), 2*time.Second)

	if n.State() != Suspect {
		t.Errorf("expected Suspect after 503, got %s", n.State())
	}
	if n.LastError() == nil {
		t.Error("expected error to be recorded")
	}
}

func TestCheckUnreachableServer(t *testing.T) {
	n := NewNode("http://127.0.0.1:19999", "", "replica", 2, 2)
	n.Check(context.Background(), 500*time.Millisecond)

	if n.State() != Suspect {
		t.Errorf("expected Suspect, got %s", n.State())
	}
}

func TestCheckSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "my-api-key", "primary", 3, 2)
	n.Check(context.Background(), 2*time.Second)

	if gotAuth != "Bearer my-api-key" {
		t.Errorf("expected 'Bearer my-api-key', got %q", gotAuth)
	}
}

func TestConcurrentChecks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "", "primary", 3, 2)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n.Check(context.Background(), 2*time.Second)
			_ = n.State()
			_ = n.IsHealthy()
		}()
	}
	wg.Wait()

	if n.State() != Healthy {
		t.Errorf("expected Healthy after concurrent checks, got %s", n.State())
	}
}

func TestLastCheckUpdated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "", "primary", 3, 2)
	before := time.Now()
	n.Check(context.Background(), 2*time.Second)
	after := time.Now()

	lc := n.LastCheck()
	if lc.Before(before) || lc.After(after) {
		t.Errorf("LastCheck %v not between %v and %v", lc, before, after)
	}
}

func TestCheckNoAuthHeader_WhenEmptyKey(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := NewNode(srv.URL, "", "primary", 3, 2)
	n.Check(context.Background(), 2*time.Second)

	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
}

func TestGetSetRole(t *testing.T) {
	n := NewNode("http://test:7700", "", "primary", 3, 2)
	if n.GetRole() != "primary" {
		t.Errorf("expected primary, got %s", n.GetRole())
	}

	n.SetRole("replica")
	if n.GetRole() != "replica" {
		t.Errorf("expected replica, got %s", n.GetRole())
	}
}

func TestOriginalRole(t *testing.T) {
	n := NewNode("http://test:7700", "", "primary", 3, 2)
	n.SetRole("replica") // simulate failover demotion
	if n.OriginalRole() != "primary" {
		t.Errorf("expected original role 'primary', got %s", n.OriginalRole())
	}
}

func TestConcurrentGetSetRole(t *testing.T) {
	n := NewNode("http://test:7700", "", "primary", 3, 2)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			n.SetRole("replica")
		}()
		go func() {
			defer wg.Done()
			_ = n.GetRole()
		}()
	}
	wg.Wait()
}

func TestInterleavedFailureRecovery(t *testing.T) {
	n := NewNode("http://test:7700", "", "primary", 3, 2)

	// Drive to Unhealthy
	for i := 0; i < 3; i++ {
		n.recordFailure(errTest)
	}
	if n.State() != Unhealthy {
		t.Fatalf("expected Unhealthy, got %s", n.State())
	}

	// One success → Suspect
	n.recordSuccess()
	if n.State() != Suspect {
		t.Fatalf("expected Suspect, got %s", n.State())
	}

	// Failure during recovery → back through Suspect
	n.recordFailure(errTest)
	// Should go back to Suspect (since we had a success which reset fails to 0, now fails=1)
	if n.State() != Suspect {
		t.Errorf("expected Suspect after interleaved failure, got %s", n.State())
	}
}

var errTest = errString("test error")

type errString string

func (e errString) Error() string { return string(e) }
