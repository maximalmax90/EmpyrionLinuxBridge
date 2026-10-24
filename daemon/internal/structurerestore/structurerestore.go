package structurerestore

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"elb/daemon/internal/config"
	"elb/daemon/internal/modclient"
	"elb/daemon/internal/structurebackup"
	"elb/daemon/internal/structurequeue"
)

type Mode string

const (
	// ModeAll — полный путь: удалить (если ещё жива) + заспавнить из
	// чертежа + контейнеры + заправка.
	ModeAll Mode = "all"
	// ModeContainers — ТОЛЬКО содержимое контейнеров уже существующей
	// структуры, без respawn'а вообще (геометрия не трогается).
	ModeContainers Mode = "containers"
	// ModeStructure — 2026-09-16, прямая просьба пользователя (3-режимная
	// модалка вместо 2): удалить+заспавнить БЕЗ восстановления контейнеров/
	// заправки — только сама структура (блоки/геометрия), содержимое
	// контейнеров/баков остаётся как есть на момент восстановления.
	ModeStructure Mode = "structure"
)

type Result struct {
	Branch    string `json:"branch"`
	NewID     int    `json:"newId,omitempty"`
	Playfield string `json:"playfield"`
}

func Restore(cfg config.Config, mod *modclient.Client, identityID, snapshotID int, mode Mode, positionPlayerID int) (Result, error) {
	entry, ok, err := resolveSnapshot(cfg, identityID, snapshotID)
	if err != nil {
		return Result{}, fmt.Errorf("поиск снимка: %w", err)
	}
	if !ok {
		return Result{}, fmt.Errorf("для структуры %d ещё нет ни одного сохранённого снимка", identityID)
	}

	loaded, err := structurequeue.LoadedPlayfields(mod)
	if err != nil {
		return Result{}, fmt.Errorf("проверка загруженных плейфилдов: %w", err)
	}
	if !loaded[entry.Playfield] {
		return Result{}, fmt.Errorf("плейфилд %s не загружен (нужен хотя бы один игрок на нём) — восстановление недоступно", entry.Playfield)
	}

	ident, identOK, err := structurebackup.GetIdentity(cfg, identityID)
	if err != nil {
		return Result{}, fmt.Errorf("поиск identity: %w", err)
	}
	exists := identOK && ident.Status == structurebackup.IdentityActive

	if mode == ModeContainers {
		if !exists {
			return Result{}, fmt.Errorf("структура %d больше не существует — доступно только полное восстановление", identityID)
		}
		if err := restoreContainers(mod, entry.Playfield, ident.EntityID, entry); err != nil {
			return Result{}, err
		}
		return Result{Branch: "containers", Playfield: entry.Playfield}, nil
	}

	if exists {
		if err := destroy(mod, entry.Playfield, ident.EntityID); err != nil {
			return Result{}, fmt.Errorf("удаление старой структуры: %w", err)
		}
	}
	newID, err := spawn(cfg, mod, entry, positionPlayerID)
	if err != nil {
		return Result{}, err
	}
	if err := structurebackup.SetIdentityEntityID(cfg, identityID, newID); err != nil {
		return Result{}, fmt.Errorf("обновление identity: %w", err)
	}

	if err := writeGeometryAndTouch(mod, entry.Playfield, newID, entry); err != nil {
		return Result{}, err
	}
	if err := writeGeometryAndTouch(mod, entry.Playfield, newID, entry); err != nil {
		return Result{}, err
	}
	if mode != ModeStructure {
		if err := restoreContainers(mod, entry.Playfield, newID, entry); err != nil {
			return Result{}, err
		}
		if err := refuel(mod, entry.Playfield, newID, entry); err != nil {
			return Result{}, err
		}
	}
	return Result{Branch: "respawn", NewID: newID, Playfield: entry.Playfield}, nil
}

func writeGeometryAndTouch(mod *modclient.Client, playfield string, id int, entry structurebackup.Entry) error {
	time.Sleep(1 * time.Second)
	if err := restoreGeometry(mod, playfield, id, entry); err != nil {
		return fmt.Errorf("восстановление геометрии: %w", err)
	}
	time.Sleep(500 * time.Millisecond)
	for i := 0; i < 10; i++ {
		if _, err := mod.Post("/structures/touch", map[string]any{"id": id}); err == nil {
			break
		}
		time.Sleep(5 * time.Second)
	}
	return nil
}

func seedEpbBase64(cfg config.Config, typeName string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(typeName)) {
	case "BA", "CV", "SV", "HV":
	default:
		return "", false
	}
	path := filepath.Join(cfg.DataRoot, "Content", "Prefabs", strings.ToUpper(typeName)+"_Player.epb")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return base64.StdEncoding.EncodeToString(data), true
}

func resolveSnapshot(cfg config.Config, identityID, snapshotID int) (structurebackup.Entry, bool, error) {
	if snapshotID > 0 {
		return structurebackup.ByID(cfg, identityID, snapshotID)
	}
	return structurebackup.Latest(cfg, identityID)
}

func destroy(mod *modclient.Client, playfield string, id int) error {
	if _, err := mod.Post("/structures/destroy", map[string]any{"id": id, "playfield": playfield}); err != nil {
		return err
	}
	for i := 0; i < 8; i++ {
		time.Sleep(500 * time.Millisecond)
		live, err := structureExistsLive(mod, playfield, id)
		if err != nil {
			continue
		}
		if !live {
			return nil
		}
	}
	return fmt.Errorf("структура %d всё ещё числится живой на %s спустя 4с после удаления — движок мог отклонить запрос (см. лог мода)", id, playfield)
}

func structureExistsLive(mod *modclient.Client, playfield string, id int) (bool, error) {
	v, err := mod.Get("/structures?playfield=" + url.QueryEscape(playfield))
	if err != nil {
		return false, err
	}
	list, ok := v.([]any)
	if !ok {
		return false, fmt.Errorf("structureExistsLive: неожиданный ответ мода: %v", v)
	}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		idF, ok := m["id"].(float64)
		if ok && int(idF) == id {
			return true, nil
		}
	}
	return false, nil
}

func spawn(cfg config.Config, mod *modclient.Client, entry structurebackup.Entry, positionPlayerID int) (int, error) {
	body := map[string]any{
		"type": entry.TypeName,
		"name": entry.Name,
	}
	if seed, ok := seedEpbBase64(cfg, entry.TypeName); ok && len(entry.GeometryBlocks) > 0 {
		body["epbBase64"] = seed
	} else {
		body["exportedEntityDatBase64"] = base64.StdEncoding.EncodeToString(entry.Blueprint)
	}
	if positionPlayerID > 0 {
		body["spawnNearPlayerId"] = positionPlayerID
	} else {
		body["playfield"] = entry.Playfield
		body["x"] = entry.PosX
		body["y"] = entry.PosY
		body["z"] = entry.PosZ
	}
	switch entry.FactionGroup {
	case 1:
		body["ownerEntityId"] = entry.FactionId
	case 0:
		if entry.FactionId != 0 {
			body["factionId"] = entry.FactionId
		}
	}
	v, err := mod.Post("/structures/spawn", body)
	if err != nil {
		return 0, fmt.Errorf("спавн из снимка: %w", err)
	}
	m, ok := v.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("спавн из снимка: неожиданный ответ мода: %v", v)
	}
	idF, ok := m["id"].(float64)
	if !ok {
		return 0, fmt.Errorf("спавн из снимка: ответ мода без id: %v", v)
	}
	return int(idF), nil
}

func restoreContainers(mod *modclient.Client, playfield string, id int, entry structurebackup.Entry) error {
	if len(entry.Containers) == 0 {
		return nil
	}
	containers := make([]map[string]any, 0, len(entry.Containers))
	for _, c := range entry.Containers {
		items := make([]map[string]any, 0, len(c.Items))
		for _, i := range c.Items {
			items = append(items, map[string]any{
				"id": i.Id, "count": i.Count, "slotIdx": i.SlotIdx, "ammo": i.Ammo, "decay": i.Decay,
			})
		}
		containers = append(containers, map[string]any{
			"x": c.Position.X, "y": c.Position.Y, "z": c.Position.Z, "items": items,
		})
	}
	_, err := mod.Post("/structures/backup/restore-containers", map[string]any{
		"id": id, "playfield": playfield, "containers": containers,
	})
	return err
}

func refuel(mod *modclient.Client, playfield string, id int, entry structurebackup.Entry) error {
	body := map[string]any{"id": id, "playfield": playfield}
	if p := tankPercent(entry.FuelTank); p != nil {
		body["fuelPercent"] = *p
	}
	if p := tankPercent(entry.OxygenTank); p != nil {
		body["oxygenPercent"] = *p
	}
	if p := tankPercent(entry.PentaxidTank); p != nil {
		body["pentaxidPercent"] = *p
	}
	if len(body) == 2 {
		return nil
	}
	_, err := mod.Post("/structures/refuel", body)
	return err
}

func restoreGeometry(mod *modclient.Client, playfield string, id int, entry structurebackup.Entry) error {
	if len(entry.GeometryBlocks) == 0 {
		return nil
	}
	blocks := make([]map[string]any, 0, len(entry.GeometryBlocks))
	for _, b := range entry.GeometryBlocks {
		blocks = append(blocks, map[string]any{
			"x": b.X, "y": b.Y, "z": b.Z, "type": b.Type, "shape": b.Shape, "rot": b.Rot, "active": b.Active,
		})
	}
	_, err := mod.Post("/structures/geometry/restore", map[string]any{
		"id": id, "playfield": playfield, "blocks": blocks,
	})
	return err
}

func tankPercent(t *modclient.TankSnapshot) *float64 {
	if t == nil || t.Capacity <= 0 {
		return nil
	}
	pct := t.Content / t.Capacity * 100
	return &pct
}
