package welcome

import (
	"testing"
	"time"

	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
)

func settingsItem(delaySeconds int) settings.WelcomeMessageItem {
	return settings.WelcomeMessageItem{DelaySeconds: delaySeconds}
}

func TestDetectJoins_FirstTickNeverWelcomes(t *testing.T) {
	players := []modclient.Player{
		{SteamId: "1", Online: true, Playfield: "Akua"},
	}
	toWelcome, next, wasFirst := detectJoins(map[string]bool{}, players, false)

	if !wasFirst {
		t.Errorf("wasFirst = false, хочу true (prev пуст, primed=false)")
	}
	if len(toWelcome) != 0 {
		t.Errorf("toWelcome = %v, хочу пусто на первом тике", toWelcome)
	}
	if !next["1"] {
		t.Errorf("next[1] = false, хочу true — игрок уже в игре, состояние должно завестись")
	}
}

func TestDetectJoins_TransitionToInGameWelcomes(t *testing.T) {
	prev := map[string]bool{"1": false}
	players := []modclient.Player{
		{SteamId: "1", Name: "TestPlayer", Online: true, Playfield: "Akua"},
	}
	toWelcome, next, wasFirst := detectJoins(prev, players, true)

	if wasFirst {
		t.Errorf("wasFirst = true, хочу false (primed=true)")
	}
	if len(toWelcome) != 1 || toWelcome[0].SteamId != "1" {
		t.Errorf("toWelcome = %v, хочу [{SteamId:1}]", toWelcome)
	}
	if !next["1"] {
		t.Errorf("next[1] = false, хочу true")
	}
}

func TestDetectJoins_OnlineWithoutPlayfieldDoesNotWelcome(t *testing.T) {
	players := []modclient.Player{
		{SteamId: "1", Online: true, Playfield: ""},
	}
	toWelcome, next, _ := detectJoins(map[string]bool{}, players, true)

	if len(toWelcome) != 0 {
		t.Errorf("toWelcome = %v, хочу пусто — playfield ещё пуст", toWelcome)
	}
	if next["1"] {
		t.Errorf("next[1] = true, хочу false — игрок ещё не в игре")
	}
}

func TestDetectJoins_AlreadyInGameDoesNotRewelcome(t *testing.T) {
	prev := map[string]bool{"1": true}
	players := []modclient.Player{
		{SteamId: "1", Online: true, Playfield: "Akua"},
	}
	toWelcome, _, _ := detectJoins(prev, players, true)

	if len(toWelcome) != 0 {
		t.Errorf("toWelcome = %v, хочу пусто — игрок уже был в игре на прошлом тике", toWelcome)
	}
}

func TestDetectJoins_RejoinAfterOfflineWelcomesAgain(t *testing.T) {
	prev := map[string]bool{"1": false}
	players := []modclient.Player{
		{SteamId: "1", Online: true, Playfield: "Akua"},
	}
	toWelcome, _, _ := detectJoins(prev, players, true)

	if len(toWelcome) != 1 {
		t.Errorf("toWelcome = %v, хочу одно приветствие на повторный вход", toWelcome)
	}
}

func TestDuePending_OnlyReturnsAndRemovesDueEntries(t *testing.T) {
	now := time.Now()
	pending := map[string][]pendingSend{
		"due1":   {{at: now.Add(-1 * time.Second)}},
		"due2":   {{at: now}},
		"notyet": {{at: now.Add(5 * time.Second)}},
	}
	due := duePending(pending, now)

	if len(due) != 2 {
		t.Fatalf("due = %v, хочу 2 записи", due)
	}
	seen := map[string]bool{}
	for _, d := range due {
		seen[d.steamID] = true
	}
	if !seen["due1"] || !seen["due2"] {
		t.Errorf("due = %v, хочу due1 и due2", due)
	}
	if _, ok := pending["due1"]; ok {
		t.Error("due1 должен быть удалён из pending")
	}
	if _, ok := pending["due2"]; ok {
		t.Error("due2 должен быть удалён из pending")
	}
	if _, ok := pending["notyet"]; !ok {
		t.Error("notyet НЕ должен быть удалён — время ещё не наступило")
	}
}

func TestDuePending_PartialSeriesKeepsRemainingEntries(t *testing.T) {
	now := time.Now()
	pending := map[string][]pendingSend{
		"p1": {
			{at: now.Add(-1 * time.Second), msg: settingsItem(1)},
			{at: now.Add(5 * time.Minute), msg: settingsItem(2)},
		},
	}
	due := duePending(pending, now)

	if len(due) != 1 || due[0].steamID != "p1" || due[0].msg.DelaySeconds != 1 {
		t.Fatalf("due = %+v, хочу одно сообщение с DelaySeconds=1", due)
	}
	remaining, ok := pending["p1"]
	if !ok || len(remaining) != 1 || remaining[0].msg.DelaySeconds != 2 {
		t.Fatalf("pending[p1] = %+v, хочу одну оставшуюся запись с DelaySeconds=2", remaining)
	}
}
