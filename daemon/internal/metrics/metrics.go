package metrics

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/safego"
	"elb/daemon/internal/sensors"
	"elb/daemon/internal/settings"
	"elb/daemon/internal/structurequeue"
	"elb/daemon/internal/sysinfo"
	"elb/daemon/internal/watchdog"

	_ "modernc.org/sqlite"
)

const sampleInterval = 15 * time.Second

const defaultRetentionDays = 7

const retentionCheckInterval = 24 * time.Hour

const vacuumEvery = 7 * 24 * time.Hour

const schemaSQL = `
CREATE TABLE IF NOT EXISTS metrics_samples (
	ts TEXT NOT NULL PRIMARY KEY,
	cpu_percent REAL NOT NULL,
	iowait_percent REAL NOT NULL,
	mem_percent REAL NOT NULL,
	swap_percent REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metrics_ts ON metrics_samples(ts);

CREATE TABLE IF NOT EXISTS metrics_disk_samples (
	ts TEXT NOT NULL,
	mountpoint TEXT NOT NULL,
	used_percent REAL NOT NULL,
	PRIMARY KEY (ts, mountpoint)
);
CREATE INDEX IF NOT EXISTS idx_metrics_disk_ts ON metrics_disk_samples(ts);

-- sensor_key = "<Chip>/<Label>" (см. internal/sensors) — тот же принцип
-- динамического ключа с независимой осью времени, что уже у mountpoint
-- выше: набор физических датчиков хоста в теории тоже может меняться.
-- Пусто на VPS/VM (нет hwmon) — это ожидаемо, не ошибка (см. package
-- internal/sensors doc).
CREATE TABLE IF NOT EXISTS metrics_sensor_samples (
	ts TEXT NOT NULL,
	sensor_key TEXT NOT NULL,
	celsius REAL NOT NULL,
	PRIMARY KEY (ts, sensor_key)
);
CREATE INDEX IF NOT EXISTS idx_metrics_sensor_ts ON metrics_sensor_samples(ts);

-- iface — тот же принцип динамического ключа с независимой осью времени,
-- что mountpoint/sensor_key выше (набор сетевых интерфейсов тоже может
-- меняться). Байты/сек, не кумулятивные счётчики (см. sysinfo.NetIface) —
-- дельта между двумя сэмплами, первый сэмпл интерфейса её ещё не знает и
-- не пишется (см. metrics.sampleOnce).
CREATE TABLE IF NOT EXISTS metrics_net_samples (
	ts TEXT NOT NULL,
	iface TEXT NOT NULL,
	rx_bytes_per_sec REAL NOT NULL,
	tx_bytes_per_sec REAL NOT NULL,
	PRIMARY KEY (ts, iface)
);
CREATE INDEX IF NOT EXISTS idx_metrics_net_ts ON metrics_net_samples(ts);

-- Одна строка на тик (не динамический ключ, как disk/sensor/net выше) —
-- ровно ОДНО общее число плейфилдов и ОДНО общее число игроков на сервер
-- целиком, не по сущности (2026-10-07, карточка "Игровая активность" —
-- прямая просьба пользователя "можно будет удобнее наблюдать динамику и
-- корреляцию с другими метриками"). Сэмплируется ТОЛЬКО пока сервер готов
-- принимать запросы мода (см. sampleOnce) — тика может не быть вовсе, пока
-- сервер остановлен, это пропуск в графике, не записанный 0.
CREATE TABLE IF NOT EXISTS metrics_activity_samples (
	ts TEXT NOT NULL PRIMARY KEY,
	playfields_active INTEGER NOT NULL,
	online_players INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_metrics_activity_ts ON metrics_activity_samples(ts);
`

type Sample struct {
	Ts            time.Time `json:"ts"`
	CPUPercent    float64   `json:"cpuPercent"`
	IOWaitPercent float64   `json:"ioWaitPercent"`
	MemPercent    float64   `json:"memPercent"`
	SwapPercent   float64   `json:"swapPercent"`
}

type DiskPoint struct {
	Ts      time.Time `json:"ts"`
	Percent float64   `json:"percent"`
}

type SensorPoint struct {
	Ts time.Time `json:"ts"`
	C  float64   `json:"c"`
}

type NetPoint struct {
	Ts    time.Time `json:"ts"`
	RxBps float64   `json:"rxBps"`
	TxBps float64   `json:"txBps"`
}

type ActivityPoint struct {
	Ts               time.Time `json:"ts"`
	PlayfieldsActive int       `json:"playfieldsActive"`
	OnlinePlayers    int       `json:"onlinePlayers"`
}

type History struct {
	Samples  []Sample                 `json:"samples"`
	Disks    map[string][]DiskPoint   `json:"disks"`
	Sensors  map[string][]SensorPoint `json:"sensors"`
	Net      map[string][]NetPoint    `json:"net"`
	Activity []ActivityPoint          `json:"activity,omitempty"`
	Governor string                   `json:"governor,omitempty"`
}

var (
	dbMu     sync.Mutex
	dbByPath = map[string]*sql.DB{}
)

type netCounter struct {
	rx, tx uint64
	at     time.Time
}

var (
	netMu   sync.Mutex
	lastNet = map[string]netCounter{}
)

func getDB(path string) (*sql.DB, error) {
	dbMu.Lock()
	defer dbMu.Unlock()

	if db, ok := dbByPath[path]; ok {
		return db, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrateDropLegacySchema(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	dbByPath[path] = db
	return db, nil
}

func migrateDropLegacySchema(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(metrics_samples)`)
	if err != nil {
		return err
	}
	legacy := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "disk_used_bytes" {
			legacy = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !legacy {
		return nil
	}
	if _, err := db.Exec(`DROP TABLE metrics_samples`); err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TABLE IF EXISTS metrics_disk_samples`); err != nil {
		return err
	}
	_, err = db.Exec(schemaSQL)
	return err
}

func retentionDays(s settings.Config) int {
	if s.Monitoring.HistoryRetentionDays <= 0 {
		return defaultRetentionDays
	}
	return s.Monitoring.HistoryRetentionDays
}

func percentOf(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(used) / float64(total)
}

func RunPoller(cfg config.Config, mod *modclient.Client) {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("metrics.RunPoller", func() {
			if err := sampleOnce(cfg, mod); err != nil {
				log.Printf("metrics: sample: %v", err)
			}
		})
	}
}

func sampleOnce(cfg config.Config, mod *modclient.Client) error {
	snap, err := sysinfo.Read(cfg.DataRoot)
	if err != nil {
		return fmt.Errorf("sysinfo: %w", err)
	}
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		return err
	}

	ts := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`
INSERT INTO metrics_samples (ts, cpu_percent, iowait_percent, mem_percent, swap_percent)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(ts) DO NOTHING`,
		ts, snap.CPUPercent, snap.IOWaitPercent,
		percentOf(snap.MemUsedBytes, snap.MemTotalBytes),
		percentOf(snap.SwapUsedBytes, snap.SwapTotalBytes))
	if err != nil {
		return fmt.Errorf("insert sample: %w", err)
	}

	mounts, err := sysinfo.ListMounts()
	if err != nil {
		return fmt.Errorf("list mounts: %w", err)
	}
	for _, m := range mounts {
		if _, err := db.Exec(`
INSERT INTO metrics_disk_samples (ts, mountpoint, used_percent)
VALUES (?, ?, ?)
ON CONFLICT(ts, mountpoint) DO NOTHING`,
			ts, m.Mountpoint, percentOf(m.UsedBytes, m.TotalBytes)); err != nil {
			return fmt.Errorf("insert disk sample (%s): %w", m.Mountpoint, err)
		}
	}

	temps, err := sensors.ReadTemps()
	if err != nil {
		log.Printf("metrics: read temps: %v", err)
	}
	for _, t := range temps {
		key := t.Chip + "/" + t.Label
		if _, err := db.Exec(`
INSERT INTO metrics_sensor_samples (ts, sensor_key, celsius)
VALUES (?, ?, ?)
ON CONFLICT(ts, sensor_key) DO NOTHING`,
			ts, key, t.C); err != nil {
			return fmt.Errorf("insert sensor sample (%s): %w", key, err)
		}
	}

	ifaces, err := sysinfo.ReadNetIfaces()
	if err != nil {
		log.Printf("metrics: read net ifaces: %v", err)
	}
	now := time.Now()
	netMu.Lock()
	for _, ifc := range ifaces {
		prev, ok := lastNet[ifc.Name]
		lastNet[ifc.Name] = netCounter{rx: ifc.RxBytes, tx: ifc.TxBytes, at: now}
		if !ok || ifc.RxBytes < prev.rx || ifc.TxBytes < prev.tx {
			continue
		}
		dt := now.Sub(prev.at).Seconds()
		if dt <= 0 {
			continue
		}
		rxBps := float64(ifc.RxBytes-prev.rx) / dt
		txBps := float64(ifc.TxBytes-prev.tx) / dt
		if _, err := db.Exec(`
INSERT INTO metrics_net_samples (ts, iface, rx_bytes_per_sec, tx_bytes_per_sec)
VALUES (?, ?, ?, ?)
ON CONFLICT(ts, iface) DO NOTHING`,
			ts, ifc.Name, rxBps, txBps); err != nil {
			netMu.Unlock()
			return fmt.Errorf("insert net sample (%s): %w", ifc.Name, err)
		}
	}
	netMu.Unlock()

	if watchdog.CachedHealth().Ready() {

		online := 0
		for _, p := range mod.CachedPlayers() {
			if p.Online {
				online++
			}
		}
		loaded, err := structurequeue.LoadedPlayfields(mod)
		if err != nil {
			log.Printf("metrics: loaded playfields: %v", err)
		} else if _, err := db.Exec(`
INSERT INTO metrics_activity_samples (ts, playfields_active, online_players)
VALUES (?, ?, ?)
ON CONFLICT(ts) DO NOTHING`,
			ts, len(loaded), online); err != nil {
			return fmt.Errorf("insert activity sample: %w", err)
		}
	}

	return nil
}

func RunRetentionPoller(cfg config.Config) {
	var lastVacuum time.Time
	ticker := time.NewTicker(retentionCheckInterval)
	defer ticker.Stop()
	for range ticker.C {
		safego.Tick("metrics.RunRetentionPoller", func() {
			s, err := settings.Load(cfg.SettingsFile)
			if err != nil {
				log.Printf("metrics: settings: %v", err)
				return
			}
			db, err := getDB(cfg.MetricsDBFile)
			if err != nil {
				log.Printf("metrics: retention: %v", err)
				return
			}
			cutoff := time.Now().UTC().Add(-time.Duration(retentionDays(s)) * 24 * time.Hour).Format(time.RFC3339Nano)
			if _, err := db.Exec(`DELETE FROM metrics_samples WHERE ts < ?`, cutoff); err != nil {
				log.Printf("metrics: retention delete: %v", err)
				return
			}
			if _, err := db.Exec(`DELETE FROM metrics_disk_samples WHERE ts < ?`, cutoff); err != nil {
				log.Printf("metrics: retention delete (disk): %v", err)
				return
			}
			if _, err := db.Exec(`DELETE FROM metrics_sensor_samples WHERE ts < ?`, cutoff); err != nil {
				log.Printf("metrics: retention delete (sensor): %v", err)
				return
			}
			if _, err := db.Exec(`DELETE FROM metrics_net_samples WHERE ts < ?`, cutoff); err != nil {
				log.Printf("metrics: retention delete (net): %v", err)
				return
			}
			if _, err := db.Exec(`DELETE FROM metrics_activity_samples WHERE ts < ?`, cutoff); err != nil {
				log.Printf("metrics: retention delete (activity): %v", err)
				return
			}
			if time.Since(lastVacuum) >= vacuumEvery {
				if _, err := db.Exec(`VACUUM;`); err != nil {
					log.Printf("metrics: vacuum: %v", err)
					return
				}
				lastVacuum = time.Now()
			}
		})
	}
}

func Query(cfg config.Config, since, until time.Time) (History, error) {
	db, err := getDB(cfg.MetricsDBFile)
	if err != nil {
		return History{}, err
	}
	sinceStr := since.UTC().Format(time.RFC3339Nano)
	untilStr := until.UTC().Format(time.RFC3339Nano)

	rows, err := db.Query(`
SELECT ts, cpu_percent, iowait_percent, mem_percent, swap_percent
FROM metrics_samples WHERE ts >= ? AND ts <= ? ORDER BY ts`, sinceStr, untilStr)
	if err != nil {
		return History{}, err
	}
	var out History
	out.Disks = map[string][]DiskPoint{}
	for rows.Next() {
		var s Sample
		var tsRaw string
		if err := rows.Scan(&tsRaw, &s.CPUPercent, &s.IOWaitPercent, &s.MemPercent, &s.SwapPercent); err != nil {
			rows.Close()
			return History{}, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsRaw)
		if err != nil {
			continue
		}
		s.Ts = ts
		out.Samples = append(out.Samples, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return History{}, err
	}
	rows.Close()

	diskRows, err := db.Query(`
SELECT ts, mountpoint, used_percent
FROM metrics_disk_samples WHERE ts >= ? AND ts <= ? ORDER BY ts`, sinceStr, untilStr)
	if err != nil {
		return History{}, err
	}
	defer diskRows.Close()
	for diskRows.Next() {
		var tsRaw, mount string
		var pct float64
		if err := diskRows.Scan(&tsRaw, &mount, &pct); err != nil {
			return History{}, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsRaw)
		if err != nil {
			continue
		}
		out.Disks[mount] = append(out.Disks[mount], DiskPoint{Ts: ts, Percent: pct})
	}
	if err := diskRows.Err(); err != nil {
		return History{}, err
	}

	out.Sensors = map[string][]SensorPoint{}
	sensorRows, err := db.Query(`
SELECT ts, sensor_key, celsius
FROM metrics_sensor_samples WHERE ts >= ? AND ts <= ? ORDER BY ts`, sinceStr, untilStr)
	if err != nil {
		return History{}, err
	}
	defer sensorRows.Close()
	for sensorRows.Next() {
		var tsRaw, key string
		var c float64
		if err := sensorRows.Scan(&tsRaw, &key, &c); err != nil {
			return History{}, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsRaw)
		if err != nil {
			continue
		}
		out.Sensors[key] = append(out.Sensors[key], SensorPoint{Ts: ts, C: c})
	}
	if err := sensorRows.Err(); err != nil {
		return History{}, err
	}

	out.Net = map[string][]NetPoint{}
	netRows, err := db.Query(`
SELECT ts, iface, rx_bytes_per_sec, tx_bytes_per_sec
FROM metrics_net_samples WHERE ts >= ? AND ts <= ? ORDER BY ts`, sinceStr, untilStr)
	if err != nil {
		return History{}, err
	}
	defer netRows.Close()
	for netRows.Next() {
		var tsRaw, iface string
		var rxBps, txBps float64
		if err := netRows.Scan(&tsRaw, &iface, &rxBps, &txBps); err != nil {
			return History{}, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsRaw)
		if err != nil {
			continue
		}
		out.Net[iface] = append(out.Net[iface], NetPoint{Ts: ts, RxBps: rxBps, TxBps: txBps})
	}
	if err := netRows.Err(); err != nil {
		return History{}, err
	}

	activityRows, err := db.Query(`
SELECT ts, playfields_active, online_players
FROM metrics_activity_samples WHERE ts >= ? AND ts <= ? ORDER BY ts`, sinceStr, untilStr)
	if err != nil {
		return History{}, err
	}
	defer activityRows.Close()
	for activityRows.Next() {
		var tsRaw string
		var a ActivityPoint
		if err := activityRows.Scan(&tsRaw, &a.PlayfieldsActive, &a.OnlinePlayers); err != nil {
			return History{}, err
		}
		ts, err := time.Parse(time.RFC3339Nano, tsRaw)
		if err != nil {
			continue
		}
		a.Ts = ts
		out.Activity = append(out.Activity, a)
	}
	if err := activityRows.Err(); err != nil {
		return History{}, err
	}

	governor, err := sensors.ReadGovernor()
	if err != nil {
		log.Printf("metrics: read governor: %v", err)
	} else {
		out.Governor = governor
	}

	return out, nil
}
