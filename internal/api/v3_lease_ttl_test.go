package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/Casper-Mars/echuan/internal/platform"
)

// leaseTimeLayout is the wire format of every timestamp the V3 API emits
// (internal/app/v3/service.go: formatTime — UTC, millisecond fixed width).
const leaseTimeLayout = "2006-01-02T15:04:05.000Z"

// TestLeaseTTLConfigReachesClaimAndWorkerDetail is D-2 (issue #165) over the real
// router: the lease a claim hands out is `now + config.lease_ttl`, and the value
// `GET /v1/workers?id=` publishes as lease_ttl_seconds is the same one. Two
// non-default durations make the "same source" visible — a hard-coded 60s (the
// service default before the wiring) or a hard-coded 90s (the config default)
// fails both rows.
func TestLeaseTTLConfigReachesClaimAndWorkerDetail(t *testing.T) {
	for _, tc := range []struct {
		name string
		ttl  time.Duration
	}{
		// The config default. Before the wiring this row failed with 60s (the
		// service's built-in default), which is D-2 itself.
		{name: "default 90s", ttl: 90 * time.Second},
		{name: "5s", ttl: 5 * time.Second},
		{name: "40s", ttl: 40 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := platform.DefaultConfig()
			cfg.LeaseTTL = tc.ttl
			a := newAPIServerWithConfig(t, cfg)

			a.seedProduct(t, "APP")
			_, payload := a.do(t, http.MethodPost, "/v1/projects", map[string]any{
				"product_id": "APP", "name": "p",
			})
			project := decodeBody[v3ProjectResponse](t, payload)
			if resp, body := a.do(t, http.MethodPost, "/v1/tasks", map[string]any{
				"project_id": project.ProjectID, "task_id": "t_lease", "kind": "dev", "title": "租约",
			}); resp.StatusCode != http.StatusCreated {
				t.Fatalf("create task = %d (%s)", resp.StatusCode, body)
			}
			a.registerWorker(t, "w_lease")

			before := time.Now().UTC()
			resp, payload := a.do(t, http.MethodPost, "/v1/tasks/claim", map[string]any{
				"worker_id": "w_lease", "kind": "dev",
			})
			after := time.Now().UTC()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("claim = %d (%s), want 200", resp.StatusCode, payload)
			}
			claimed := decodeBody[v3ClaimResponse](t, payload)
			if claimed.Task.LeaseUntil == nil {
				t.Fatalf("claim response has no lease_until: %s", payload)
			}
			until, err := time.Parse(leaseTimeLayout, *claimed.Task.LeaseUntil)
			if err != nil {
				t.Fatalf("parse lease_until %q: %v", *claimed.Task.LeaseUntil, err)
			}
			// The tolerance absorbs one round trip plus the millisecond
			// truncation of formatTime; it is far below the gap between the
			// built-in 60s and either fixture, so a wrong source cannot pass.
			if until.Before(before.Add(tc.ttl-time.Second)) || until.After(after.Add(tc.ttl+time.Second)) {
				t.Errorf("lease_until = %s, want now+%s (claim window %s..%s)",
					*claimed.Task.LeaseUntil, tc.ttl, before.Format(leaseTimeLayout), after.Format(leaseTimeLayout))
			}

			// Same source on the registry detail: the published rhythm names the
			// duration the claim above actually used.
			resp, payload = a.do(t, http.MethodGet, "/v1/workers?id=w_lease", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("worker detail = %d (%s), want 200", resp.StatusCode, payload)
			}
			detail := decodeBody[v3WorkerDetailResponse](t, payload)
			if want := int64(tc.ttl / time.Second); detail.LeaseTTLSeconds != want {
				t.Errorf("detail lease_ttl_seconds = %d, want %d (config lease_ttl)", detail.LeaseTTLSeconds, want)
			}
		})
	}
}
