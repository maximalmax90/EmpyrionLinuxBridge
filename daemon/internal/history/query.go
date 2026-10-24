package history

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"

	"elb/daemon/internal/config"
)

type Event struct {
	ID           int64                   `json:"id"`
	Ts           string                  `json:"ts"`
	Kind         string                  `json:"kind"`
	EntityKey    string                  `json:"entityKey"`
	Name         string                  `json:"name"`
	Playfield    string                  `json:"playfield"`
	Pos          Vec3                    `json:"pos"`
	Changed      map[string]changedField `json:"changed,omitempty"`
	IsFirst      bool                    `json:"isFirst"`
	IsDeleted    bool                    `json:"isDeleted"`
	OnlyPosition bool                    `json:"onlyPosition"`

	Distance float64 `json:"distance"`
}

type Vec3 struct{ X, Y, Z float64 }

type Query struct {
	AnchorKind string
	AnchorKey  string

	Kind string

	Playfield   string
	FromTs      string
	ToTs        string
	MaxDistance float64
	HideFirst   bool

	HideOnlyPosition bool

	Limit  int
	Offset int
}

type Result struct {
	Events []Event `json:"events"`
	Total  int     `json:"total"`
}

func Around(cfg config.Config, q Query) (Result, error) {
	db, err := getDB(cfg)
	if err != nil {
		return Result{}, err
	}

	if q.AnchorKind == "" || q.AnchorKey == "" {
		return listEvents(db, q)
	}

	track, err := anchorTrack(db, q.AnchorKind, q.AnchorKey, q.ToTs)
	if err != nil {
		return Result{}, fmt.Errorf("anchor track: %w", err)
	}
	if len(track) == 0 {
		return Result{}, nil
	}

	sqlWhere := `ts >= ? AND ts <= ?`
	args := []any{q.FromTs, q.ToTs}
	if q.Playfield != "" {
		sqlWhere += ` AND playfield = ?`
		args = append(args, q.Playfield)
	}
	if q.HideFirst {
		sqlWhere += ` AND is_first = 0`
	}
	if q.HideOnlyPosition {
		sqlWhere += ` AND only_position = 0`
	}
	rows, err := db.Query(`
SELECT id, ts, kind, entity_key, name, playfield, pos_x, pos_y, pos_z, changed, is_first, is_deleted, only_position
FROM history_events
WHERE `+sqlWhere+`
ORDER BY ts`, args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return Result{}, err
	}

	var out []Event
	for _, e := range events {
		anchor, ok := anchorPositionAt(track, e.Ts)
		if !ok {
			continue
		}

		if e.Playfield != anchor.playfield {
			continue
		}
		e.Distance = anchor.pos.distanceTo(vec3{e.Pos.X, e.Pos.Y, e.Pos.Z})
		if e.Distance > q.MaxDistance {
			continue
		}
		out = append(out, e)
	}

	total := len(out)
	page := paginateTail(out, q.Limit, q.Offset)
	return Result{Events: page, Total: total}, nil
}

func paginateTail(events []Event, limit, offset int) []Event {
	total := len(events)
	if limit <= 0 || offset < 0 || offset >= total {
		return nil
	}
	end := total - offset
	start := end - limit
	if start < 0 {
		start = 0
	}
	return events[start:end]
}

func listEvents(db *sql.DB, q Query) (Result, error) {
	sqlWhere := `ts >= ? AND ts <= ?`
	args := []any{q.FromTs, q.ToTs}
	if q.Kind != "" {
		sqlWhere += ` AND kind = ?`
		args = append(args, q.Kind)
	}
	if q.Playfield != "" {
		sqlWhere += ` AND playfield = ?`
		args = append(args, q.Playfield)
	}
	if q.HideFirst {
		sqlWhere += ` AND is_first = 0`
	}
	if q.HideOnlyPosition {
		sqlWhere += ` AND only_position = 0`
	}

	var total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM history_events WHERE `+sqlWhere, args...).Scan(&total); err != nil {
		return Result{}, err
	}

	limit := q.Limit
	if limit <= 0 {
		limit = total
	}
	pageArgs := append(append([]any{}, args...), limit, q.Offset)
	rows, err := db.Query(`
SELECT id, ts, kind, entity_key, name, playfield, pos_x, pos_y, pos_z, changed, is_first, is_deleted, only_position
FROM history_events
WHERE `+sqlWhere+`
ORDER BY ts DESC LIMIT ? OFFSET ?`, pageArgs...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return Result{}, err
	}

	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return Result{Events: events, Total: total}, nil
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		var changedRaw string
		var isFirst, isDeleted, onlyPosition int
		if err := rows.Scan(&e.ID, &e.Ts, &e.Kind, &e.EntityKey, &e.Name, &e.Playfield,
			&e.Pos.X, &e.Pos.Y, &e.Pos.Z, &changedRaw, &isFirst, &isDeleted, &onlyPosition); err != nil {
			return nil, err
		}
		e.IsFirst, e.IsDeleted, e.OnlyPosition = isFirst != 0, isDeleted != 0, onlyPosition != 0
		if changedRaw != "" && changedRaw != "{}" {
			if err := json.Unmarshal([]byte(changedRaw), &e.Changed); err != nil {
				return nil, fmt.Errorf("unmarshal changed: %w", err)
			}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func CountsByKind(cfg config.Config, kind string) (map[string]int, error) {
	db, err := getDB(cfg)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT entity_key, COUNT(*) FROM history_events WHERE kind = ? GROUP BY entity_key`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var key string
		var count int
		if err := rows.Scan(&key, &count); err != nil {
			return nil, err
		}
		out[key] = count
	}
	return out, rows.Err()
}

type trackPoint struct {
	ts        string
	pos       vec3
	playfield string
}

func anchorTrack(db *sql.DB, kind, key, upToTs string) ([]trackPoint, error) {
	rows, err := db.Query(`
SELECT ts, pos_x, pos_y, pos_z, playfield FROM history_events
WHERE kind = ? AND entity_key = ? AND ts <= ?
ORDER BY ts`, kind, key, upToTs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var track []trackPoint
	for rows.Next() {
		var p trackPoint
		if err := rows.Scan(&p.ts, &p.pos.X, &p.pos.Y, &p.pos.Z, &p.playfield); err != nil {
			return nil, err
		}
		track = append(track, p)
	}
	return track, rows.Err()
}

func anchorPositionAt(track []trackPoint, at string) (trackPoint, bool) {
	i := sort.Search(len(track), func(i int) bool { return track[i].ts > at })
	if i == 0 {
		return trackPoint{}, false
	}
	return track[i-1], true
}
