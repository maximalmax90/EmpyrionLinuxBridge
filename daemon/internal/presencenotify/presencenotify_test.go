package presencenotify

import (
	"testing"

	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
)

func TestFormatMessage_ColorsOnlineGreenOfflineRed(t *testing.T) {
	p := modclient.Player{Name: "TestPlayer"}

	online := formatMessage(p, true)
	wantOnline := "[c][ffffff]Игрок [b][ffcc00]TestPlayer[/b][ffffff] [33cc33]онлайн[/c]"
	if online != wantOnline {
		t.Errorf("online:\n got %q\nwant %q", online, wantOnline)
	}

	offline := formatMessage(p, false)
	wantOffline := "[c][ffffff]Игрок [b][ffcc00]TestPlayer[/b][ffffff] [ff3333]оффлайн[/c]"
	if offline != wantOffline {
		t.Errorf("offline:\n got %q\nwant %q", offline, wantOffline)
	}
}

func settingsWith(admins, moderators []string) settings.Config {
	var s settings.Config
	s.Chatbot.AdminSteamIds = admins
	s.Chatbot.ModeratorSteamIds = moderators
	return s
}

func TestDetectTransitions_FirstTickNeverFires(t *testing.T) {
	players := []modclient.Player{{SteamId: "1", Online: true, Playfield: "Akua"}}
	joined, left, next, wasFirst := detectTransitions(map[string]bool{}, players, false)

	if !wasFirst {
		t.Error("wasFirst = false, хочу true")
	}
	if len(joined) != 0 || len(left) != 0 {
		t.Errorf("joined=%v left=%v, хочу оба пустые на первом тике", joined, left)
	}
	if !next["1"] {
		t.Error("next[1] = false, хочу true — состояние должно завестись")
	}
}

func TestDetectTransitions_JoinDetected(t *testing.T) {
	prev := map[string]bool{"1": false}
	players := []modclient.Player{{SteamId: "1", Name: "A", Online: true, Playfield: "Akua"}}
	joined, left, _, _ := detectTransitions(prev, players, true)

	if len(joined) != 1 || joined[0].SteamId != "1" {
		t.Errorf("joined = %v, хочу [{SteamId:1}]", joined)
	}
	if len(left) != 0 {
		t.Errorf("left = %v, хочу пусто", left)
	}
}

func TestDetectTransitions_LeaveDetected(t *testing.T) {
	prev := map[string]bool{"1": true}
	players := []modclient.Player{{SteamId: "1", Name: "A", Online: false, Playfield: ""}}
	joined, left, _, _ := detectTransitions(prev, players, true)

	if len(left) != 1 || left[0].SteamId != "1" {
		t.Errorf("left = %v, хочу [{SteamId:1}]", left)
	}
	if len(joined) != 0 {
		t.Errorf("joined = %v, хочу пусто", joined)
	}
}

func TestDetectTransitions_LeaveViaEmptyPlayfieldOnlineTrue(t *testing.T) {
	prev := map[string]bool{"1": true}
	players := []modclient.Player{{SteamId: "1", Online: true, Playfield: ""}}
	_, left, _, _ := detectTransitions(prev, players, true)

	if len(left) != 1 {
		t.Errorf("left = %v, хочу одно срабатывание — playfield опустел", left)
	}
}

func TestDetectTransitions_NoChangeNoEvents(t *testing.T) {
	prev := map[string]bool{"1": true}
	players := []modclient.Player{{SteamId: "1", Online: true, Playfield: "Akua"}}
	joined, left, _, _ := detectTransitions(prev, players, true)

	if len(joined) != 0 || len(left) != 0 {
		t.Errorf("joined=%v left=%v, хочу оба пустые — состояние не менялось", joined, left)
	}
}

func TestRecipientSteamIds_DedupesAdminsAndModerators(t *testing.T) {
	s := settingsWith([]string{"a", "b"}, []string{"b", "c"})
	ids := recipientSteamIds(s)

	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Errorf("дубликат %s в %v", id, ids)
		}
		seen[id] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !seen[want] {
			t.Errorf("%s отсутствует в %v", want, ids)
		}
	}
	if len(ids) != 3 {
		t.Errorf("len(ids) = %d, хочу 3", len(ids))
	}
}
