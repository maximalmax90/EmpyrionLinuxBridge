package blockclass

import (
	"os"
	"path/filepath"
	"testing"

	"elb/daemon/internal/config"
)

const sampleBlocksConfig = `# comment line mentioning Block Name: ShouldBeIgnored
{ Block Name: DetectorCVT2
  Info: bkiDetectorT2, display: true
  WeaponItem: DetectorCVT2Weapon
  Group: cpgDetector
  Material: WeaponLarge
}
{ Block Name: HullFullSmall
  Group: stone
}
{ Block Name: HullExtendedSmall7, Ref: HullFullSmall
  Mass: 10
}
{ Block Name: OrphanExtended7, Ref: NeverDefinedBase
  Mass: 5
}
{ Block Name: PlainNoGroupNoRef
  Mass: 1
}
{ Block Id: 458, Name: ThrusterMSRoundNormal2x2
  Group: Thruster
  Mass: 20
}
{ Block Id: 1396, Name: AlienFullLarge, Ref: HullFullSmall
  Mass: 30
}
{ Block Name: FollowsLegacyBlock
  Group: cpgWeapon
}
{ Block Id: 326, Name: TurretMSCannonRetract, Ref: HullFullSmall       #TurretPlayer CV Cannon
  Mass: 15
}
 { Block Id: 1986, Name: HeavyWindowFBottomInv, Ref: HullFullSmall
  Mass: 2
}
`

func testCfg(t *testing.T) config.Config {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "Content", "Scenarios", "TestScenario", "Content", "Configuration")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "BlocksConfig.ecf"), []byte(sampleBlocksConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	yamlPath := filepath.Join(root, "dedicated.yaml")
	if err := os.WriteFile(yamlPath, []byte("GameConfig:\r\n    CustomScenario: TestScenario\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return config.Config{DataRoot: root, DedicatedYAML: yamlPath}
}

func TestGroups_DirectGroup(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if groups["DetectorCVT2"] != "cpgDetector" {
		t.Fatalf("DetectorCVT2 group = %q, want cpgDetector", groups["DetectorCVT2"])
	}
}

func TestGroups_ResolvesThroughRef(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if groups["HullExtendedSmall7"] != "stone" {
		t.Fatalf("HullExtendedSmall7 group = %q, want stone (унаследовано через Ref от HullFullSmall)", groups["HullExtendedSmall7"])
	}
}

func TestGroups_DanglingRefResolvesEmpty(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g, ok := groups["OrphanExtended7"]; !ok || g != "" {
		t.Fatalf("OrphanExtended7 group = %q (ok=%v), want \"\" (Ref ссылается на неопределённый блок)", g, ok)
	}
}

func TestGroups_NoGroupNoRefIsEmpty(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g := groups["PlainNoGroupNoRef"]; g != "" {
		t.Fatalf("PlainNoGroupNoRef group = %q, want \"\"", g)
	}
}

func TestGroups_IgnoresCommentLine(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := groups["ShouldBeIgnored"]; ok {
		t.Fatal("строка внутри комментария не должна была распознаться как Block Name")
	}
}

func TestGroups_LegacyIdFormDoesNotLeakGroupToPrecedingBlock(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g := groups["PlainNoGroupNoRef"]; g != "" {
		t.Fatalf("PlainNoGroupNoRef group = %q, want \"\" (Group следующего легаси-блока не должна утекать сюда)", g)
	}
	if g := groups["ThrusterMSRoundNormal2x2"]; g != "Thruster" {
		t.Fatalf("ThrusterMSRoundNormal2x2 group = %q, want Thruster", g)
	}
}

func TestGroups_LegacyIdFormResolvesThroughRef(t *testing.T) {
	cfg := testCfg(t)
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if g := groups["AlienFullLarge"]; g != "stone" {
		t.Fatalf("AlienFullLarge group = %q, want stone (унаследовано через Ref от HullFullSmall)", g)
	}
	if g := groups["FollowsLegacyBlock"]; g != "cpgWeapon" {
		t.Fatalf("FollowsLegacyBlock group = %q, want cpgWeapon (не должна была пострадать от соседнего легаси-блока)", g)
	}
}

func TestNamesByID_ReturnsLegacyIds(t *testing.T) {
	cfg := testCfg(t)
	ids, err := NamesByID(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ids[458] != "ThrusterMSRoundNormal2x2" {
		t.Fatalf("ids[458] = %q, want ThrusterMSRoundNormal2x2", ids[458])
	}
	if ids[1396] != "AlienFullLarge" {
		t.Fatalf("ids[1396] = %q, want AlienFullLarge", ids[1396])
	}
	if _, ok := ids[0]; ok {
		t.Fatal("DetectorCVT2 (форма Block Name без Id) не должна попадать в NamesByID")
	}
}

func TestNamesByID_TrailingInlineCommentAfterRef(t *testing.T) {
	cfg := testCfg(t)
	ids, err := NamesByID(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ids[326] != "TurretMSCannonRetract" {
		t.Fatalf("ids[326] = %q, want TurretMSCannonRetract", ids[326])
	}
}

func TestNamesByID_LeadingWhitespaceBeforeBrace(t *testing.T) {
	cfg := testCfg(t)
	ids, err := NamesByID(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ids[1986] != "HeavyWindowFBottomInv" {
		t.Fatalf("ids[1986] = %q, want HeavyWindowFBottomInv", ids[1986])
	}
}

func TestNamesByID_MissingFileReturnsEmptyNoError(t *testing.T) {
	cfg := config.Config{DataRoot: t.TempDir()}
	ids, err := NamesByID(cfg)
	if err != nil {
		t.Fatalf("err = %v, want nil (отсутствие BlocksConfig.ecf — не ошибка)", err)
	}
	if len(ids) != 0 {
		t.Fatalf("ids = %v, want пусто", ids)
	}
}

func TestGroups_MissingFileReturnsEmptyNoError(t *testing.T) {
	cfg := config.Config{DataRoot: t.TempDir()}
	groups, err := Groups(cfg)
	if err != nil {
		t.Fatalf("err = %v, want nil (отсутствие BlocksConfig.ecf — не ошибка)", err)
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %v, want пусто", groups)
	}
}

func TestIsDeviceGroup(t *testing.T) {
	cases := map[string]bool{
		"":            false,
		"stone":       false,
		"sand":        false,
		"cpgDetector": true,
		"cpgWeapon":   true,
		"cpgThruster": true,
		"Thruster":    true,
		"Shield":      true,
		"Constructor": true,
		"Drill":       true,
		"DrillTurret": true,
		"RCS":         true,
		"SomeUnknown": false,
	}
	for group, want := range cases {
		if got := IsDeviceGroup(group); got != want {
			t.Errorf("IsDeviceGroup(%q) = %v, want %v", group, got, want)
		}
	}
}
