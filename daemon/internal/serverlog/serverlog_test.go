package serverlog

import (
	"strings"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		line string
		want Category
	}{
		{`06-11:22:16.318 19_22 -LOG- CHAT ServerInfo/Global from -1/Faction.0 to -1/Faction.0 (admin): 'тест персистентности чата'`, CategoryChat},
		{`06-11:35:39.707 19_35 -LOG- CHAT Player/SinglePlayer from 43007/Faction.1 to 10080/Faction.0 (-): 'приват'`, CategoryChat},
		{`06-11:22:05.324 19_22 -LOG- {ElbMod} ElbMod: initialized, HTTP API on http://127.0.0.1:31914/`, CategoryMod},
		{`06-11:35:10.791 19_35 -LOG- {:ElbMod} ElbMod: Game_Event Event_Playfield_Loaded seq=0 pending=False`, CategoryMod},
		{`05-11:00:31.134 19_00 -ERR- Task_CommWriterRPC (cl=TestPlayer, ch=4):`, CategoryError},
		{`05-11:00:31.141 19_00 -EXC- System.NullReferenceException: Object reference not set to an instance of an object`, CategoryError},
		{`06-12:13:23.989 20_13 -WRN- Eos: 00007F64BDAFEED0: HTTP request timed out after 30.00 seconds URL=https://api.epicgames.dev/telemetry/data`, CategoryWarning},
		{`05-11:00:31.136 19_00 -LOG- [CM] Player CId=1, EId=3080, 76561198019604899/=/'TestPlayer' disconnected`, CategoryPlayer},
		{`05-11:00:31.131 19_00 -LOG- Client CId=1, EId=3080, 76561198019604899/=/'TestPlayer' disconnected from Akua, now empty -> delayed unload enqueued`, CategoryPlayer},
		{`06-11:35:09.697 19_35 -LOG- Requesting pf server with PID 656 to load playfield 'Tallodar'`, CategoryPlayfield},
		{`06-11:35:10.785 19_35 -LOG- Playfield server (PID 656) loaded playfield 'Tallodar' in 1,1 sec`, CategoryPlayfield},
		{`06-11:21:56.293 19_21 -LOG- Save game version v1.19.2, Build 5150`, CategorySave},
		{`06-11:22:04.423 19_22 -LOG- Faction statistics enabled`, CategoryNoise},
		{`05-09:27:23.836 17_27 -LOG- Client CId=1, EId=-1, 76561198019604899//'TestPlayer' requests 1781 files from server`, CategoryNoise},
		{`Mono path[0] = 'Z:/server/EMP/DedicatedServer/EmpyrionDedicated_Data/Managed'`, CategoryRaw},
		{`  at Assembly-CSharp.AspectHelper.UnregisterView (System.Int32 ) [0x0001f] in <7dbae4da6c7d45ebb5f35305a3f0b1f3>:0 `, CategoryRaw},
		{``, CategoryRaw},
	}

	for _, c := range cases {
		if got := Classify(c.line); got != c.want {
			t.Errorf("Classify(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestClassifySeverity(t *testing.T) {
	now := time.Date(2026, 6, 11, 20, 0, 0, 0, time.Local)
	cases := []struct {
		line    string
		wantCat Category
		wantSev Severity
	}{
		{`06-11:22:05.324 19_22 -ERR- {ElbMod} ElbMod: необработанное исключение в обработчике чата`, CategoryMod, SeverityCritical},
		{`05-11:00:31.134 19_00 -ERR- Task_CommWriterRPC (cl=TestPlayer, ch=4):`, CategoryError, SeverityCritical},
		{`05-11:00:31.141 19_00 -EXC- System.NullReferenceException: Object reference not set to an instance of an object`, CategoryError, SeverityCritical},
		{`06-12:13:23.989 20_13 -WRN- Eos: HTTP request timed out`, CategoryWarning, SeverityWarning},
		{`06-11:22:04.423 19_22 -LOG- Faction statistics enabled`, CategoryNoise, SeverityNone},
	}
	for _, c := range cases {
		gotCat, gotSev, _, _ := classify(c.line, now)
		if gotCat != c.wantCat || gotSev != c.wantSev {
			t.Errorf("classify(%q) = (%q, %q), want (%q, %q)", c.line, gotCat, gotSev, c.wantCat, c.wantSev)
		}
	}
}

func TestResolveLineTime(t *testing.T) {
	cases := []struct {
		name                string
		day                 int
		hour, minute        int
		now                 time.Time
		wantYear, wantMonth int
	}{
		{"тот же день", 11, 19, 22, time.Date(2026, 6, 11, 20, 0, 0, 0, time.Local), 2026, 6},
		{"декабрь-январь: перенос на прошлый год", 31, 23, 50, time.Date(2027, 1, 1, 0, 5, 0, 0, time.Local), 2026, 12},
		{"живой баг 2026-09-13: день 13 не месяц", 13, 11, 13, time.Date(2026, 9, 13, 19, 13, 0, 0, time.Local), 2026, 9},
		{"день не помещается в текущий месяц (30 февраля)", 30, 10, 0, time.Date(2026, 2, 28, 12, 0, 0, 0, time.Local), 2026, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolveLineTime(c.day, c.hour, c.minute, c.now)
			if got.Year() != c.wantYear || int(got.Month()) != c.wantMonth || got.Day() != c.day || got.Hour() != c.hour || got.Minute() != c.minute {
				t.Errorf("resolveLineTime(%d,%d,%d, now=%v) = %v, want year=%d month=%d day=%d %02d:%02d",
					c.day, c.hour, c.minute, c.now, got, c.wantYear, c.wantMonth, c.day, c.hour, c.minute)
			}
		})
	}
}

func TestParseLines_DisplayTextDropsUTCField(t *testing.T) {
	text := `18-04:02:02.912 12_02 -LOG- {:ElbMod} ElbMod: Game_Event Event_GlobalStructure`
	lines := ParseLines(text)
	if len(lines) != 1 {
		t.Fatalf("ожидал 1 строку, получил %d: %+v", len(lines), lines)
	}
	want := `-LOG- {:ElbMod} ElbMod: Game_Event Event_GlobalStructure`
	if lines[0].Text != want {
		t.Errorf("Text = %q, want %q", lines[0].Text, want)
	}
}

func TestParseLines_SkipsEmpty(t *testing.T) {
	text := "a\n\n06-11:21:56.293 19_21 -LOG- Save game version v1.19.2, Build 5150\n\n"
	lines := ParseLines(text)
	if len(lines) != 2 {
		t.Fatalf("ожидал 2 строки (пустые пропущены), получил %d: %+v", len(lines), lines)
	}
	if lines[1].Category != CategorySave {
		t.Errorf("вторая строка: категория %q, ожидал %q", lines[1].Category, CategorySave)
	}
}

func TestParseLines_StripsTrailingCR(t *testing.T) {
	text := "06-11:21:56.293 19_21 -LOG- Save game version v1.19.2, Build 5150\r\n"
	lines := ParseLines(text)
	if len(lines) != 1 {
		t.Fatalf("ожидал 1 строку, получил %d: %+v", len(lines), lines)
	}
	if strings.HasSuffix(lines[0].Text, "\r") {
		t.Errorf("Text не должен заканчиваться на '\\r': %q", lines[0].Text)
	}
}
