package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthony-hopkins/tomb/internal/auth"
	"github.com/anthony-hopkins/tomb/internal/blizzard"
)

// TestMemOwners: characters recorded at sign-in are read back by their
// roster key, a character that signs in under another account follows it,
// and blank names are ignored.
func TestMemOwners(t *testing.T) {
	m := &MemOwners{}
	_ = m.Record(context.Background(), 1, []blizzard.Character{{Name: "Nekromoo", RealmSlug: "Area-52"}, {Name: "Lazzlowe", RealmSlug: "elune"}, {Name: "", RealmSlug: "elune"}})
	_ = m.Record(context.Background(), 2, []blizzard.Character{{Name: "Lazzlowe", RealmSlug: "elune"}})
	got, _ := m.Owners(context.Background())
	if len(got) != 2 || got[OwnerKey("area-52", "nekromoo")] != 1 || got["elune/lazzlowe"] != 2 {
		t.Errorf("owners = %v", got)
	}
}

// TestAdmitRecordsOwners: a member's sign-in records the account's
// characters; a non-member's does not.
func TestAdmitRecordsOwners(t *testing.T) {
	owners := &MemOwners{}
	core, _ := sessionedCore(t, true)
	core.Deps.Owners = owners
	rec := httptest.NewRecorder()
	if !core.Admit(rec, httptest.NewRequest(http.MethodGet, "/", nil), auth.Session{User: auth.User{ID: 7, BattleTag: "Tester#1234"}, AccessToken: "token"}) {
		t.Fatal("a member was not admitted")
	}
	got, _ := owners.Owners(context.Background())
	if len(got) == 0 {
		t.Fatal("nothing recorded at sign-in")
	}
	for _, id := range got {
		if id != 7 {
			t.Errorf("recorded for user %d, want 7", id)
		}
	}

	strangers := &MemOwners{}
	core, _ = sessionedCore(t, false)
	core.Deps.Owners = strangers
	core.Admit(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil), auth.Session{User: auth.User{ID: 8}, AccessToken: "token"})
	if got, _ := strangers.Owners(context.Background()); len(got) != 0 {
		t.Errorf("a non-member's characters were recorded: %v", got)
	}
}
