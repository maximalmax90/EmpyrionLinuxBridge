package metrics

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/sysinfo"
)

func testCfg(t *testing.T) config.Config {
	dir := t.TempDir()
	return config.Config{
		DataRoot:      dir,
		MetricsDBFile: filepath.Join(dir, "elb", "metrics.db"),
	}
}

func testMod(t *testing.T) *modclient.Client {
	t.Helper()
	return modclient.New("http://127.0.0.1:1")
}

func insertSample(t *testing.T, cfg config.Config, ts time.Time, cpu float64) {
	t.Helper()
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
INSERT INTO metrics_samples (ts, cpu_percent, iowait_percent, mem_percent, swap_percent)
VALUES (?, ?, 0, 0, 0)`, ts.UTC().Format(time.RFC3339Nano), cpu)
	if err != nil {
		t.Fatal(err)
	}
}

func insertDiskSample(t *testing.T, cfg config.Config, ts time.Time, mount string, pct float64) {
	t.Helper()
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
INSERT INTO metrics_disk_samples (ts, mountpoint, used_percent)
VALUES (?, ?, ?)`, ts.UTC().Format(time.RFC3339Nano), mount, pct)
	if err != nil {
		t.Fatal(err)
	}
}

func insertSensorSample(t *testing.T, cfg config.Config, ts time.Time, key string, c float64) {
	t.Helper()
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
INSERT INTO metrics_sensor_samples (ts, sensor_key, celsius)
VALUES (?, ?, ?)`, ts.UTC().Format(time.RFC3339Nano), key, c)
	if err != nil {
		t.Fatal(err)
	}
}

func insertNetSample(t *testing.T, cfg config.Config, ts time.Time, iface string, rxBps, txBps float64) {
	t.Helper()
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
INSERT INTO metrics_net_samples (ts, iface, rx_bytes_per_sec, tx_bytes_per_sec)
VALUES (?, ?, ?, ?)`, ts.UTC().Format(time.RFC3339Nano), iface, rxBps, txBps)
	if err != nil {
		t.Fatal(err)
	}
}

func insertActivitySample(t *testing.T, cfg config.Config, ts time.Time, playfieldsActive, onlinePlayers int) {
	t.Helper()
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
INSERT INTO metrics_activity_samples (ts, playfields_active, online_players)
VALUES (?, ?, ?)`, ts.UTC().Format(time.RFC3339Nano), playfieldsActive, onlinePlayers)
	if err != nil {
		t.Fatal(err)
	}
}

func TestQuery_ReturnsPerIfaceNetHistory(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()
	insertNetSample(t, cfg, now.Add(-10*time.Minute), "eth0", 1000, 2000)
	insertNetSample(t, cfg, now.Add(-5*time.Minute), "eth0", 1500, 2500)
	insertNetSample(t, cfg, now.Add(-5*time.Minute), "wlan0", 300, 400)

	insertNetSample(t, cfg, now.Add(-2*time.Hour), "eth0", 1, 1)

	got, err := Query(cfg, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Net["eth0"]) != 2 {
		t.Fatalf("got %d точек для eth0, want 2: %+v", len(got.Net["eth0"]), got.Net["eth0"])
	}
	if got.Net["eth0"][0].RxBps != 1000 || got.Net["eth0"][0].TxBps != 2000 ||
		got.Net["eth0"][1].RxBps != 1500 || got.Net["eth0"][1].TxBps != 2500 {
		t.Fatalf("неверный порядок/значения: %+v", got.Net["eth0"])
	}
	if len(got.Net["wlan0"]) != 1 || got.Net["wlan0"][0].RxBps != 300 || got.Net["wlan0"][0].TxBps != 400 {
		t.Fatalf("неверные данные для wlan0: %+v", got.Net["wlan0"])
	}
}

func TestSampleOnce_WritesNetSampleOnSecondTick(t *testing.T) {
	ifaces, err := sysinfo.ReadNetIfaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(ifaces) == 0 {
		t.Skip("на этой машине нет ни одного реального сетевого интерфейса кроме loopback")
	}
	cfg := testCfg(t)
	if err := sampleOnce(cfg, testMod(t)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := sampleOnce(cfg, testMod(t)); err != nil {
		t.Fatal(err)
	}
	got, err := Query(cfg, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Net) == 0 {
		t.Fatalf("после двух тиков нет ни одной строки сетевой истории, ожидали хотя бы один реальный интерфейс")
	}
}

func TestQuery_ReturnsActivityHistory(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()
	insertActivitySample(t, cfg, now.Add(-10*time.Minute), 3, 5)
	insertActivitySample(t, cfg, now.Add(-5*time.Minute), 4, 7)

	insertActivitySample(t, cfg, now.Add(-2*time.Hour), 99, 99)

	got, err := Query(cfg, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Activity) != 2 {
		t.Fatalf("got %d точек активности, want 2: %+v", len(got.Activity), got.Activity)
	}
	if got.Activity[0].PlayfieldsActive != 3 || got.Activity[0].OnlinePlayers != 5 ||
		got.Activity[1].PlayfieldsActive != 4 || got.Activity[1].OnlinePlayers != 7 {
		t.Fatalf("неверный порядок/значения: %+v", got.Activity)
	}
}

func TestSampleOnce_SkipsActivityWhenServerNotReady(t *testing.T) {
	cfg := testCfg(t)
	if err := sampleOnce(cfg, testMod(t)); err != nil {
		t.Fatal(err)
	}
	got, err := Query(cfg, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Activity) != 0 {
		t.Fatalf("ожидали 0 строк активности при неготовом сервере, получили %+v", got.Activity)
	}
}

func TestQuery_ReturnsPerSensorHistory(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()
	insertSensorSample(t, cfg, now.Add(-10*time.Minute), "coretemp/Package id 0", 35.0)
	insertSensorSample(t, cfg, now.Add(-5*time.Minute), "coretemp/Package id 0", 36.5)
	insertSensorSample(t, cfg, now.Add(-5*time.Minute), "nvme/Composite", 33.85)

	insertSensorSample(t, cfg, now.Add(-2*time.Hour), "coretemp/Package id 0", 1.0)

	got, err := Query(cfg, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Sensors["coretemp/Package id 0"]) != 2 {
		t.Fatalf("got %d точек для coretemp/Package id 0, want 2: %+v", len(got.Sensors["coretemp/Package id 0"]), got.Sensors["coretemp/Package id 0"])
	}
	if got.Sensors["coretemp/Package id 0"][0].C != 35.0 || got.Sensors["coretemp/Package id 0"][1].C != 36.5 {
		t.Fatalf("неверный порядок/значения: %+v", got.Sensors["coretemp/Package id 0"])
	}
	if len(got.Sensors["nvme/Composite"]) != 1 || got.Sensors["nvme/Composite"][0].C != 33.85 {
		t.Fatalf("неверные данные для nvme/Composite: %+v", got.Sensors["nvme/Composite"])
	}
}

func TestQuery_ReturnsOnlySamplesSinceCutoff(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()
	insertSample(t, cfg, now.Add(-2*time.Hour), 10)
	insertSample(t, cfg, now.Add(-30*time.Minute), 20)
	insertSample(t, cfg, now, 30)

	got, err := Query(cfg, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 2 {
		t.Fatalf("got %d samples, want 2 (только -30m и now должны попасть): %+v", len(got.Samples), got.Samples)
	}
	if got.Samples[0].CPUPercent != 20 || got.Samples[1].CPUPercent != 30 {
		t.Fatalf("неверный порядок/значения: %+v", got.Samples)
	}
}

func TestQuery_OrderedByTimeAscending(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()

	insertSample(t, cfg, now, 3)
	insertSample(t, cfg, now.Add(-10*time.Minute), 1)
	insertSample(t, cfg, now.Add(-5*time.Minute), 2)

	got, err := Query(cfg, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 3 {
		t.Fatalf("got %d samples, want 3", len(got.Samples))
	}
	for i, want := range []float64{1, 2, 3} {
		if got.Samples[i].CPUPercent != want {
			t.Fatalf("index %d: CPUPercent=%v, want %v (порядок нарушен): %+v", i, got.Samples[i].CPUPercent, want, got.Samples)
		}
	}
}

func TestQuery_ReturnsPerMountDiskHistory(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()
	insertDiskSample(t, cfg, now.Add(-10*time.Minute), "/", 42.5)
	insertDiskSample(t, cfg, now.Add(-5*time.Minute), "/", 43.1)
	insertDiskSample(t, cfg, now.Add(-5*time.Minute), "/server", 88.0)

	insertDiskSample(t, cfg, now.Add(-2*time.Hour), "/", 1.0)

	got, err := Query(cfg, now.Add(-1*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Disks["/"]) != 2 {
		t.Fatalf("got %d точек для /, want 2: %+v", len(got.Disks["/"]), got.Disks["/"])
	}
	if got.Disks["/"][0].Percent != 42.5 || got.Disks["/"][1].Percent != 43.1 {
		t.Fatalf("неверный порядок/значения для /: %+v", got.Disks["/"])
	}
	if len(got.Disks["/server"]) != 1 || got.Disks["/server"][0].Percent != 88.0 {
		t.Fatalf("неверные данные для /server: %+v", got.Disks["/server"])
	}
}

func TestSampleOnce_WritesReadableRow(t *testing.T) {
	cfg := testCfg(t)
	if err := sampleOnce(cfg, testMod(t)); err != nil {
		t.Fatal(err)
	}
	got, err := Query(cfg, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(got.Samples))
	}
}

func TestSampleOnce_WritesAllRealMounts(t *testing.T) {
	cfg := testCfg(t)
	if err := sampleOnce(cfg, testMod(t)); err != nil {
		t.Fatal(err)
	}
	got, err := Query(cfg, time.Now().Add(-time.Minute), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Disks) == 0 {
		t.Fatalf("got no disk history, want at least one real mount (\"/\" always exists)")
	}
}

func TestRetentionDays_ZeroMeansDefault(t *testing.T) {
	zero := settings.Config{Monitoring: settings.Monitoring{HistoryRetentionDays: 0}}
	if got := retentionDays(zero); got != defaultRetentionDays {
		t.Fatalf("retentionDays(0) = %d, want default %d", got, defaultRetentionDays)
	}
	thirty := settings.Config{Monitoring: settings.Monitoring{HistoryRetentionDays: 30}}
	if got := retentionDays(thirty); got != 30 {
		t.Fatalf("retentionDays(30) = %d, want 30", got)
	}
}

func TestRunRetentionPoller_DeletesOldRowsKeepsRecent(t *testing.T) {
	cfg := testCfg(t)
	now := time.Now().UTC()
	insertSample(t, cfg, now.Add(-10*24*time.Hour), 1)
	insertSample(t, cfg, now.Add(-1*time.Hour), 2)
	insertDiskSample(t, cfg, now.Add(-10*24*time.Hour), "/", 1)
	insertDiskSample(t, cfg, now.Add(-1*time.Hour), "/", 2)
	insertSensorSample(t, cfg, now.Add(-10*24*time.Hour), "coretemp/Package id 0", 1)
	insertSensorSample(t, cfg, now.Add(-1*time.Hour), "coretemp/Package id 0", 2)
	insertNetSample(t, cfg, now.Add(-10*24*time.Hour), "eth0", 1, 1)
	insertNetSample(t, cfg, now.Add(-1*time.Hour), "eth0", 2, 2)
	insertActivitySample(t, cfg, now.Add(-10*24*time.Hour), 1, 1)
	insertActivitySample(t, cfg, now.Add(-1*time.Hour), 2, 2)

	s := settings.Config{Monitoring: settings.Monitoring{HistoryRetentionDays: 0}}

	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC().Add(-time.Duration(retentionDays(s)) * 24 * time.Hour).Format(time.RFC3339Nano)
	if _, err := db.Exec(`DELETE FROM metrics_samples WHERE ts < ?`, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM metrics_disk_samples WHERE ts < ?`, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM metrics_sensor_samples WHERE ts < ?`, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM metrics_net_samples WHERE ts < ?`, cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM metrics_activity_samples WHERE ts < ?`, cutoff); err != nil {
		t.Fatal(err)
	}

	got, err := Query(cfg, now.Add(-30*24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Samples) != 1 {
		t.Fatalf("после ретеншн-прунинга осталось %d строк, want 1 (старая должна удалиться): %+v", len(got.Samples), got.Samples)
	}
	if got.Samples[0].CPUPercent != 2 {
		t.Fatalf("осталась не та строка: %+v", got.Samples)
	}
	if len(got.Disks["/"]) != 1 || got.Disks["/"][0].Percent != 2 {
		t.Fatalf("ретеншн не почистил metrics_disk_samples: %+v", got.Disks["/"])
	}
	if len(got.Sensors["coretemp/Package id 0"]) != 1 || got.Sensors["coretemp/Package id 0"][0].C != 2 {
		t.Fatalf("ретеншн не почистил metrics_sensor_samples: %+v", got.Sensors["coretemp/Package id 0"])
	}
	if len(got.Net["eth0"]) != 1 || got.Net["eth0"][0].RxBps != 2 {
		t.Fatalf("ретеншн не почистил metrics_net_samples: %+v", got.Net["eth0"])
	}
	if len(got.Activity) != 1 || got.Activity[0].PlayfieldsActive != 2 {
		t.Fatalf("ретеншн не почистил metrics_activity_samples: %+v", got.Activity)
	}
}

func TestMigrateDropLegacySchema_RecreatesOnOldColumn(t *testing.T) {
	cfg := testCfg(t)
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`DROP TABLE metrics_samples`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE metrics_samples (ts TEXT PRIMARY KEY, cpu_percent REAL, iowait_percent REAL, mem_used_bytes INTEGER, mem_total_bytes INTEGER, swap_used_bytes INTEGER, disk_used_bytes INTEGER, disk_total_bytes INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO metrics_samples VALUES ('2020-01-01T00:00:00Z', 1, 2, 3, 4, 5, 6, 7)`); err != nil {
		t.Fatal(err)
	}

	if err := migrateDropLegacySchema(db); err != nil {
		t.Fatal(err)
	}

	var memPercentColExists bool
	rows, err := db.Query(`PRAGMA table_info(metrics_samples)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		if name == "mem_percent" {
			memPercentColExists = true
		}
	}
	if !memPercentColExists {
		t.Fatal("после миграции ожидалась новая колонка mem_percent")
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM metrics_samples`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("после миграции старые данные должны быть отброшены, got count=%d", count)
	}
}
