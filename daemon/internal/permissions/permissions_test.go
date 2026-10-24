package permissions

import (
	"reflect"
	"sort"
	"testing"
)

func TestParseRoles(t *testing.T) {
	text := "Players with 'Admin' permission:\n" +
		"Players with 'Moderator' permission:\n" +
		"Players with 'GameMaster' permission:\n" +
		"  76561198019604899 'TestPlayer'\n" +
		"\n" +
		"Thread 'TelnetClientReceive_127.0.0.1:12345' ThreadId=1 ManagedId=1\n"

	roles := parseRoles(text)

	if got := roles["76561198019604899"]; got != "gamemaster" {
		t.Errorf("roles[76561198019604899] = %q, хочу %q", got, "gamemaster")
	}
	if len(roles) != 1 {
		t.Errorf("len(roles) = %d, хочу 1 (роли: %v)", len(roles), roles)
	}
}

func TestIdsToElevate(t *testing.T) {
	t.Run("отсутствие в roles тоже требует elevate", func(t *testing.T) {
		got := idsToElevate([]string{"1", "2"}, map[string]string{"1": "moderator"}, "admin")
		want := []string{"1", "2"}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("уже admin — пропускаем", func(t *testing.T) {
		got := idsToElevate([]string{"1", "2"}, map[string]string{"1": "admin", "2": "admin"}, "admin")
		if len(got) != 0 {
			t.Errorf("got %v, want пусто", got)
		}
	})

	t.Run("уже moderator — пропускаем", func(t *testing.T) {
		got := idsToElevate([]string{"1", "2"}, map[string]string{"1": "moderator", "2": "moderator"}, "moderator")
		if len(got) != 0 {
			t.Errorf("got %v, want пусто", got)
		}
	})

	t.Run("пустой список — пусто, без паники на nil roles", func(t *testing.T) {
		if got := idsToElevate(nil, nil, "admin"); len(got) != 0 {
			t.Errorf("got %v, want пусто", got)
		}
	})

	t.Run("частичное совпадение — только несовпавшие", func(t *testing.T) {
		got := idsToElevate([]string{"1", "2", "3"}, map[string]string{"1": "admin", "2": "gamemaster"}, "admin")
		want := []string{"2", "3"}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("admin не считается moderator", func(t *testing.T) {
		got := idsToElevate([]string{"1"}, map[string]string{"1": "admin"}, "moderator")
		if len(got) != 1 {
			t.Errorf("got %v, want [1] — admin не равно moderator", got)
		}
	})
}

func TestModeratorElevateTargets(t *testing.T) {
	t.Run("без пересечения — весь список модераторов", func(t *testing.T) {
		got := moderatorElevateTargets([]string{"a"}, []string{"1", "2"})
		want := []string{"1", "2"}
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("пересечение — admin побеждает, исключается из целей moderator", func(t *testing.T) {
		got := moderatorElevateTargets([]string{"1"}, []string{"1", "2"})
		want := []string{"2"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("пустые списки — пусто, без паники", func(t *testing.T) {
		if got := moderatorElevateTargets(nil, nil); len(got) != 0 {
			t.Errorf("got %v, want пусто", got)
		}
	})
}

func TestFilterOnline(t *testing.T) {
	t.Run("офлайн-игроки отфильтрованы", func(t *testing.T) {
		got := filterOnline([]string{"1", "2", "3"}, map[string]bool{"1": true, "3": true})
		want := []string{"1", "3"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("никто не онлайн — пусто", func(t *testing.T) {
		got := filterOnline([]string{"1", "2"}, map[string]bool{})
		if len(got) != 0 {
			t.Errorf("got %v, want пусто", got)
		}
	})

	t.Run("пустой список id — пусто, без паники", func(t *testing.T) {
		if got := filterOnline(nil, map[string]bool{"1": true}); len(got) != 0 {
			t.Errorf("got %v, want пусто", got)
		}
	})
}
