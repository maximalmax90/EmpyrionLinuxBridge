package playerstats

import (
	"path/filepath"
	"testing"

	"elb/daemon/internal/config"
)

func testConfig(t *testing.T) config.Config {
	return config.Config{PlayerStatsDBFile: filepath.Join(t.TempDir(), "playerstats.db")}
}

func insertRow(t *testing.T, cfg config.Config, steamID string, kills, died int) {
	t.Helper()
	db, err := getDB(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		INSERT INTO player_stats (
			steam_id, health, health_max, food, food_max, oxygen, oxygen_max,
			stamina, stamina_max, radiation, radiation_max, body_temp, body_temp_max,
			faction_group, faction_id, faction_role, credits, exp, upgrade, kills, died, updated_at
		) VALUES (?, 0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0, ?, ?, '2026-10-02T00:00:00Z')`,
		steamID, kills, died)
	if err != nil {
		t.Fatal(err)
	}
}

func TestGet_RoundTripsKillsDied(t *testing.T) {
	cfg := testConfig(t)
	insertRow(t, cfg, "76561197996618618", 42, 7)

	s, found, err := Get(cfg, "76561197996618618")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("found = false, хочу true")
	}
	if s.Kills != 42 || s.Died != 7 {
		t.Errorf("Kills=%d Died=%d, хочу 42/7", s.Kills, s.Died)
	}
}

func TestCachedKillsDied_EmptyBeforeRefresh(t *testing.T) {

	if _, ok := CachedKillsDied()["нет-такого-steamid"]; ok {
		t.Fatal("несуществующий steamId не должен быть в кэше")
	}
}

func TestRefreshKillsDiedCache_PopulatesCachedKillsDied(t *testing.T) {
	cfg := testConfig(t)
	insertRow(t, cfg, "111", 10, 1)
	insertRow(t, cfg, "222", 0, 0)

	if err := refreshKillsDiedCache(cfg); err != nil {
		t.Fatal(err)
	}
	got := CachedKillsDied()
	if got["111"] != (KillsDied{Kills: 10, Died: 1}) {
		t.Errorf("got[111] = %+v, хочу {10 1}", got["111"])
	}
	if got["222"] != (KillsDied{Kills: 0, Died: 0}) {
		t.Errorf("got[222] = %+v, хочу {0 0}", got["222"])
	}
}
