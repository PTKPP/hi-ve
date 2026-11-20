package entitlements

import (
	"testing"
	"time"
)

func TestEntitlementWindow(t *testing.T) {
	effective := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expiry := effective.Add(time.Hour)
	for _, tc := range []struct {
		now   time.Time
		state string
	}{
		{effective.Add(-time.Microsecond), "scheduled"}, {effective, "active"}, {expiry.Add(-time.Microsecond), "active"}, {expiry, "expired"},
	} {
		result := Result{Effective: effective, Expiry: expiry}
		result.at(tc.now)
		if result.State != tc.state {
			t.Fatalf("%v: got %s, want %s", tc.now, result.State, tc.state)
		}
	}
}
