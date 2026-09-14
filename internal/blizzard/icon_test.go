package blizzard

import "testing"

// TestAllowedIconURL guards the one check standing between an icon and a silent
// Content-Security-Policy failure.
//
// An icon served from an origin img-src does not allow renders as a broken
// image and a console line nobody reads. Checking the host means the icon is
// dropped with a reason recorded instead.
func TestAllowedIconURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"blizzard's render host", "https://render.worldofwarcraft.com/us/icons/56/inv_helm.jpg", true},
		{"a regional subdomain", "https://eu.render.worldofwarcraft.com/icons/56/x.jpg", true},

		{"plain http", "http://render.worldofwarcraft.com/us/icons/56/x.jpg", false},
		{"a different host entirely", "https://cdn.example.com/icons/x.jpg", false},
		{"suffix confusion", "https://render.worldofwarcraft.com.evil.example/x.jpg", false},
		{"missing the dot separator", "https://evilrender.worldofwarcraft.com/x.jpg", false},
		{"empty", "", false},
		{"not a url", "::::", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AllowedIconURL(tc.url); got != tc.want {
				t.Errorf("AllowedIconURL(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}

// TestSortEquipmentUsesTheGamesOrder: head down to weapons, with anything
// unrecognised at the end rather than dropped.
func TestSortEquipmentUsesTheGamesOrder(t *testing.T) {
	items := []EquippedItem{
		{SlotType: "MAIN_HAND", Name: "axe"},
		{SlotType: "SOMETHING_NEW", Name: "future"},
		{SlotType: "HEAD", Name: "helm"},
		{SlotType: "FINGER_2", Name: "ring2"},
		{SlotType: "FINGER_1", Name: "ring1"},
	}
	SortEquipment(items)

	var got []string
	for _, it := range items {
		got = append(got, it.Name)
	}
	want := []string{"helm", "ring1", "ring2", "axe", "future"}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}
