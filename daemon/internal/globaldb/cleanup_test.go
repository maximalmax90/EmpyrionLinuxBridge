package globaldb

import (
	"reflect"
	"testing"
)

func TestGroupDuplicates(t *testing.T) {
	t.Run("три дубля одного имени — keep наибольший, остальные на удаление", func(t *testing.T) {
		got := groupDuplicates([]entityNameID{
			{ID: 2080, Name: "TestPlayer"},
			{ID: 6008, Name: "TestPlayer"},
			{ID: 154057, Name: "TestPlayer"},
		})
		want := []DuplicateGroup{{Name: "TestPlayer", Keep: 154057, Remove: []int{2080, 6008}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("одна запись на имя — не группа, ничего не удаляем", func(t *testing.T) {
		got := groupDuplicates([]entityNameID{
			{ID: 6139, Name: "TestPlayer2"},
			{ID: 154057, Name: "TestPlayer"},
		})
		if len(got) != 0 {
			t.Errorf("got %+v, хочу пусто", got)
		}
	})

	t.Run("несколько независимых групп — каждая своя, не смешиваются", func(t *testing.T) {
		got := groupDuplicates([]entityNameID{
			{ID: 100, Name: "A"},
			{ID: 200, Name: "A"},
			{ID: 6139, Name: "TestPlayer2"},
			{ID: 50, Name: "B"},
			{ID: 60, Name: "B"},
			{ID: 70, Name: "B"},
		})
		want := []DuplicateGroup{
			{Name: "A", Keep: 200, Remove: []int{100}},
			{Name: "B", Keep: 70, Remove: []int{50, 60}},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("порядок ID во входных данных не важен — keep всё равно максимум", func(t *testing.T) {
		got := groupDuplicates([]entityNameID{
			{ID: 154057, Name: "TestPlayer"},
			{ID: 2080, Name: "TestPlayer"},
			{ID: 6008, Name: "TestPlayer"},
		})
		want := []DuplicateGroup{{Name: "TestPlayer", Keep: 154057, Remove: []int{2080, 6008}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})
}
