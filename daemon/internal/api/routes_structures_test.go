package api

import (
	"testing"

	"elb/daemon/internal/structurebackup"
)

func TestEntityIDToIdentityID_PrefersMostRecentOnCollision(t *testing.T) {

	active := []structurebackup.Identity{
		{ID: 26, Name: "FST_SV", EntityID: 21745},
		{ID: 3, Name: "Малое судно (МС)", EntityID: 21745},
	}
	got := entityIDToIdentityID(active)
	if got[21745] != 26 {
		t.Fatalf("entityIDToIdentityID()[21745] = %d, want 26 (самая свежая identity)", got[21745])
	}
}

func TestEntityIDToIdentityID_NoCollisionMapsEachEntity(t *testing.T) {
	active := []structurebackup.Identity{
		{ID: 26, Name: "FST_SV", EntityID: 21745},
		{ID: 27, Name: "Забуханка", EntityID: 86001},
	}
	got := entityIDToIdentityID(active)
	if got[21745] != 26 || got[86001] != 27 {
		t.Fatalf("got %+v, want {21745:26, 86001:27}", got)
	}
}

func TestSortBlockBreakdownItems(t *testing.T) {
	items := []blockBreakdownItem{
		{Key: "b", Name: "Б", Count: 5},
		{Key: "a", Name: "А", Count: 10},
		{Key: "c", Name: "В", Count: 10},
	}
	sortBlockBreakdownItems(items)
	want := []string{"А", "В", "Б"}
	for i, name := range want {
		if items[i].Name != name {
			t.Fatalf("items[%d].Name = %q, want %q (полный срез: %+v)", i, items[i].Name, name, items)
		}
	}
}
