package itemstacks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParse_AmmoTypeInheritance(t *testing.T) {
	ecf := `{ +Item Id: 11, Name: MultiTool
  StackSize: 1
  Category: Weapons/Items
  Children:
  {
    Child 0
    Class: Ranged
    AmmoType: MultiCharge, display: true
  }
}
{ +Item Id: 31, Name: MultiToolT2, Ref: MultiTool
  Meshfile: Entities/Items/Weapons/MultiToolT2Prefab
  TechTreeParent: MultiTool
}
{ +Item Id: 3, Name: Pistol
  StackSize: 1
  Children:
  {
    Child 0
    Class: Ranged
    AmmoType: 9mmBullet, display: true
  }
}
{ +Item Id: 4, Name: PistolT2, Ref: Pistol
}
{ +Item Id: 5, Name: NoAmmoNoRef
  StackSize: 3
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "ItemsConfig.ecf")
	if err := os.WriteFile(path, []byte(ecf), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, ammoTypes, _, err := parse(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := ammoTypes["MultiTool"]; got != "MultiCharge" {
		t.Errorf("ammoTypes[MultiTool] = %q, хочу MultiCharge", got)
	}
	if got := ammoTypes["MultiToolT2"]; got != "MultiCharge" {
		t.Errorf("ammoTypes[MultiToolT2] (унаследовано от MultiTool через Ref) = %q, хочу MultiCharge", got)
	}
	if got := ammoTypes["PistolT2"]; got != "9mmBullet" {
		t.Errorf("ammoTypes[PistolT2] (унаследовано от Pistol через Ref) = %q, хочу 9mmBullet", got)
	}
	if _, ok := ammoTypes["NoAmmoNoRef"]; ok {
		t.Errorf("ammoTypes[NoAmmoNoRef] не должно быть записи (нет ни своего AmmoType, ни Ref)")
	}
}
