package platform

import "testing"

// TestHomePathFollowsRegistrationOrder covers where a signed-in member lands.
//
// "/" used to redirect to a hardcoded "/app/dashboard". It now goes to the
// FIRST registered app, so moving the front page is a matter of reordering the
// list in cmd/tomb — where app composition already lives — rather than editing
// the core. The core has no business knowing which app happens to be home
// (Principle II).
func TestHomePathFollowsRegistrationOrder(t *testing.T) {
	tests := []struct {
		name     string
		registry []AppMeta
		want     string
	}{
		{
			name: "the first app is home",
			registry: []AppMeta{
				{Slug: "guild", RoutePrefix: "/app/guild"},
				{Slug: "dashboard", RoutePrefix: "/app/dashboard"},
			},
			want: "/app/guild",
		},
		{
			name: "reordering moves home, with no core change",
			registry: []AppMeta{
				{Slug: "dashboard", RoutePrefix: "/app/dashboard"},
				{Slug: "guild", RoutePrefix: "/app/guild"},
			},
			want: "/app/dashboard",
		},
		{
			// A redirect to "/" from "/" would loop. A bare landing page is the
			// less bad failure.
			name:     "no apps at all does not loop",
			registry: nil,
			want:     "/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Core{registry: tc.registry}
			if got := c.homePath(); got != tc.want {
				t.Errorf("homePath() = %q, want %q", got, tc.want)
			}
		})
	}
}
