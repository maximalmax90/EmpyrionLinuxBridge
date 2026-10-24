package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite"
)

const schemaSQL = `
CREATE TABLE IF NOT EXISTS users (
	username TEXT PRIMARY KEY,
	password_hash TEXT NOT NULL,
	must_change_password INTEGER NOT NULL DEFAULT 0,
	role TEXT NOT NULL DEFAULT 'admin',
	theme TEXT NOT NULL DEFAULT 'light',
	locale TEXT NOT NULL DEFAULT 'ru'
);
CREATE TABLE IF NOT EXISTS sessions (
	token TEXT PRIMARY KEY,
	username TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS item_mapping (
	name TEXT PRIMARY KEY,
	id INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS admin_players (
	steam_id TEXT PRIMARY KEY,
	name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS moderator_players (
	steam_id TEXT PRIMARY KEY,
	name TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS item_mapping_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS icons (
	scenario TEXT NOT NULL,
	name TEXT NOT NULL,
	data BLOB NOT NULL,
	synced_at TEXT NOT NULL,
	PRIMARY KEY (scenario, name)
);
CREATE TABLE IF NOT EXISTS panel_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

type Store struct {
	db *sql.DB
}

const (
	RoleAdmin     = "admin"
	RoleModerator = "moderator"
)

func validRole(role string) bool {
	return role == RoleAdmin || role == RoleModerator
}

func migrateUsersColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(users)`)
	if err != nil {
		return fmt.Errorf("pragma table_info(users): %w", err)
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	add := func(colDDL, name string) error {
		if have[name] {
			return nil
		}
		_, err := db.Exec(`ALTER TABLE users ADD COLUMN ` + colDDL)
		return err
	}
	if err := add(`role TEXT NOT NULL DEFAULT 'admin'`, "role"); err != nil {
		return fmt.Errorf("add users.role: %w", err)
	}
	if err := add(`theme TEXT NOT NULL DEFAULT 'light'`, "theme"); err != nil {
		return fmt.Errorf("add users.theme: %w", err)
	}
	if err := add(`locale TEXT NOT NULL DEFAULT 'ru'`, "locale"); err != nil {
		return fmt.Errorf("add users.locale: %w", err)
	}
	return nil
}

func dropFlatIconsCache(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(icons)`)
	if err != nil {
		return fmt.Errorf("pragma table_info(icons): %w", err)
	}
	defer rows.Close()

	hasScenarioCol := false
	tableExists := false
	for rows.Next() {
		tableExists = true
		var cid int
		var name, ctype string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == "scenario" {
			hasScenarioCol = true
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if tableExists && !hasScenarioCol {
		if _, err := db.Exec(`DROP TABLE icons`); err != nil {
			return fmt.Errorf("drop legacy icons table: %w", err)
		}
	}
	return nil
}

func Open(dbFile string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbFile), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", filepath.Dir(dbFile), err)
	}
	db, err := sql.Open("sqlite", dbFile)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dbFile, err)
	}

	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		return nil, fmt.Errorf("pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000;`); err != nil {
		return nil, fmt.Errorf("pragma busy_timeout: %w", err)
	}
	if err := dropFlatIconsCache(db); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		return nil, fmt.Errorf("schema: %w", err)
	}
	if err := migrateUsersColumns(db); err != nil {
		return nil, err
	}

	s := &Store{db: db}
	if err := s.seedDefaultUser(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) seedDefaultUser() error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users (username, password_hash, must_change_password, role) VALUES (?, ?, 1, ?)`, "admin", string(hash), RoleAdmin)
	return err
}

type User struct {
	Username           string
	MustChangePassword bool
	Role               string
	Theme              string
	Locale             string
}

func (s *Store) Authenticate(username, password string) (*User, error) {
	var hash string
	u := User{Username: username}
	err := s.db.QueryRow(`SELECT password_hash, must_change_password, role, theme, locale FROM users WHERE username = ?`, username).
		Scan(&hash, &u.MustChangePassword, &u.Role, &u.Theme, &u.Locale)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("authenticate: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, nil
	}
	return &u, nil
}

func (s *Store) SetPassword(oldUsername, newUsername, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var role, theme, locale string
	if err := tx.QueryRow(`SELECT role, theme, locale FROM users WHERE username = ?`, oldUsername).Scan(&role, &theme, &locale); err != nil {
		return err
	}

	if newUsername != oldUsername {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, newUsername).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			return fmt.Errorf("пользователь %q уже существует", newUsername)
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE username = ?`, oldUsername); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO users (username, password_hash, must_change_password, role, theme, locale) VALUES (?, ?, 0, ?, ?, ?)`,
		newUsername, string(hash), role, theme, locale); err != nil {
		return err
	}

	if _, err := tx.Exec(`DELETE FROM sessions WHERE username = ?`, oldUsername); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetPreferences(username, theme, locale string) error {
	_, err := s.db.Exec(`UPDATE users SET theme = ?, locale = ? WHERE username = ?`, theme, locale, username)
	return err
}

type PlayerColumns struct {
	Faction     bool `json:"faction"`
	Coords      bool `json:"coords"`
	Kills       bool `json:"kills"`
	Died        bool `json:"died"`
	Credits     bool `json:"credits"`
	Exp         bool `json:"exp"`
	Ping        bool `json:"ping"`
	LastOnline  bool `json:"lastOnline"`
	OnlineHours bool `json:"onlineHours"`

	QuickLinks bool `json:"quickLinks"`
}

func defaultPlayerColumns() PlayerColumns {
	return PlayerColumns{
		Faction: true, Coords: true, Kills: true, Died: true, Credits: true,
		Exp: true, Ping: true, LastOnline: true, OnlineHours: true, QuickLinks: false,
	}
}

var (
	playerColumnsMu    sync.RWMutex
	playerColumnsCache *PlayerColumns
)

func (s *Store) PlayerColumns() PlayerColumns {
	playerColumnsMu.RLock()
	if playerColumnsCache != nil {
		v := *playerColumnsCache
		playerColumnsMu.RUnlock()
		return v
	}
	playerColumnsMu.RUnlock()

	pc := defaultPlayerColumns()
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM panel_settings WHERE key = 'player_columns'`).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &pc)
	}
	playerColumnsMu.Lock()
	playerColumnsCache = &pc
	playerColumnsMu.Unlock()
	return pc
}

func (s *Store) SetPlayerColumns(pc PlayerColumns) error {
	raw, err := json.Marshal(pc)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO panel_settings (key, value) VALUES ('player_columns', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		string(raw)); err != nil {
		return err
	}
	playerColumnsMu.Lock()
	playerColumnsCache = &pc
	playerColumnsMu.Unlock()
	return nil
}

type ModeratorPermissions struct {
	ChatbotBasic bool `json:"chatbotBasic"`
	PanelBasic   bool `json:"panelBasic"`
	Structures   bool `json:"structures"`
	History      bool `json:"history"`
	ChatHistory  bool `json:"chatHistory"`

	Journal bool `json:"journal"`
}

func defaultModeratorPermissions() ModeratorPermissions {
	return ModeratorPermissions{ChatbotBasic: true, PanelBasic: true, Structures: true, History: true, ChatHistory: true, Journal: true}
}

var (
	moderatorPermissionsMu    sync.RWMutex
	moderatorPermissionsCache *ModeratorPermissions
)

func (s *Store) ModeratorPermissions() ModeratorPermissions {
	moderatorPermissionsMu.RLock()
	if moderatorPermissionsCache != nil {
		v := *moderatorPermissionsCache
		moderatorPermissionsMu.RUnlock()
		return v
	}
	moderatorPermissionsMu.RUnlock()

	p := defaultModeratorPermissions()
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM panel_settings WHERE key = 'moderator_permissions'`).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &p)
	}
	moderatorPermissionsMu.Lock()
	moderatorPermissionsCache = &p
	moderatorPermissionsMu.Unlock()
	return p
}

func (s *Store) SetModeratorPermissions(p ModeratorPermissions) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO panel_settings (key, value) VALUES ('moderator_permissions', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		string(raw)); err != nil {
		return err
	}
	moderatorPermissionsMu.Lock()
	moderatorPermissionsCache = &p
	moderatorPermissionsMu.Unlock()
	return nil
}

type ChatChannelColors struct {
	Global       string `json:"global"`
	Faction      string `json:"faction"`
	Alliance     string `json:"alliance"`
	Server       string `json:"server"`
	Singleplayer string `json:"singleplayer"`
}

type ChatSenderColors struct {
	Server string `json:"server"`
	Admin  string `json:"admin"`
	VIP    string `json:"vip"`
}

type ChatBannerColors struct {
	Alarm   string `json:"alarm"`
	Message string `json:"message"`
	Info    string `json:"info"`
}

type ChatColors struct {
	Light ChatChannelColors `json:"light"`
	Dark  ChatChannelColors `json:"dark"`

	LightSenders ChatSenderColors `json:"lightSenders"`
	DarkSenders  ChatSenderColors `json:"darkSenders"`

	LightBanner ChatBannerColors `json:"lightBanner"`
	DarkBanner  ChatBannerColors `json:"darkBanner"`
}

func defaultChatColors() ChatColors {
	white := ChatChannelColors{Global: "#ffffff", Faction: "#ffffff", Alliance: "#ffffff", Server: "#ffffff", Singleplayer: "#ffffff"}

	whiteBanner := ChatBannerColors{Alarm: "#ffffff", Message: "#ffffff", Info: "#ffffff"}
	return ChatColors{Light: white, Dark: white, LightBanner: whiteBanner, DarkBanner: whiteBanner}
}

func isValidHexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

var (
	chatColorsMu    sync.RWMutex
	chatColorsCache *ChatColors
)

func (s *Store) ChatColors() ChatColors {
	chatColorsMu.RLock()
	if chatColorsCache != nil {
		v := *chatColorsCache
		chatColorsMu.RUnlock()
		return v
	}
	chatColorsMu.RUnlock()

	c := defaultChatColors()
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM panel_settings WHERE key = 'chat_colors'`).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &c)
	}
	chatColorsMu.Lock()
	chatColorsCache = &c
	chatColorsMu.Unlock()
	return c
}

func (s *Store) SetChatColors(c ChatColors) error {
	cur := s.ChatColors()
	sanitize := func(newVal, curVal string) string {
		if isValidHexColor(newVal) {
			return newVal
		}
		return curVal
	}
	sanitizeOptional := func(newVal, curVal string) string {
		if newVal == "" || isValidHexColor(newVal) {
			return newVal
		}
		return curVal
	}
	c.Light.Global = sanitize(c.Light.Global, cur.Light.Global)
	c.Light.Faction = sanitize(c.Light.Faction, cur.Light.Faction)
	c.Light.Alliance = sanitize(c.Light.Alliance, cur.Light.Alliance)
	c.Light.Server = sanitize(c.Light.Server, cur.Light.Server)
	c.Light.Singleplayer = sanitize(c.Light.Singleplayer, cur.Light.Singleplayer)
	c.Dark.Global = sanitize(c.Dark.Global, cur.Dark.Global)
	c.Dark.Faction = sanitize(c.Dark.Faction, cur.Dark.Faction)
	c.Dark.Alliance = sanitize(c.Dark.Alliance, cur.Dark.Alliance)
	c.Dark.Server = sanitize(c.Dark.Server, cur.Dark.Server)
	c.Dark.Singleplayer = sanitize(c.Dark.Singleplayer, cur.Dark.Singleplayer)
	c.LightSenders.Server = sanitizeOptional(c.LightSenders.Server, cur.LightSenders.Server)
	c.LightSenders.Admin = sanitizeOptional(c.LightSenders.Admin, cur.LightSenders.Admin)
	c.LightSenders.VIP = sanitizeOptional(c.LightSenders.VIP, cur.LightSenders.VIP)
	c.DarkSenders.Server = sanitizeOptional(c.DarkSenders.Server, cur.DarkSenders.Server)
	c.DarkSenders.Admin = sanitizeOptional(c.DarkSenders.Admin, cur.DarkSenders.Admin)
	c.DarkSenders.VIP = sanitizeOptional(c.DarkSenders.VIP, cur.DarkSenders.VIP)

	c.LightBanner.Alarm = sanitize(c.LightBanner.Alarm, cur.LightBanner.Alarm)
	c.LightBanner.Message = sanitize(c.LightBanner.Message, cur.LightBanner.Message)
	c.LightBanner.Info = sanitize(c.LightBanner.Info, cur.LightBanner.Info)
	c.DarkBanner.Alarm = sanitize(c.DarkBanner.Alarm, cur.DarkBanner.Alarm)
	c.DarkBanner.Message = sanitize(c.DarkBanner.Message, cur.DarkBanner.Message)
	c.DarkBanner.Info = sanitize(c.DarkBanner.Info, cur.DarkBanner.Info)

	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO panel_settings (key, value) VALUES ('chat_colors', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		string(raw)); err != nil {
		return err
	}
	chatColorsMu.Lock()
	chatColorsCache = &c
	chatColorsMu.Unlock()
	return nil
}

type ItemRenderConfig struct {
	LocalizedNames bool `json:"localizedNames"`

	ShowDescriptions bool `json:"showDescriptions"`

	RenderTags bool `json:"renderTags"`

	DarkBackground bool `json:"darkBackground"`
}

func defaultItemRenderConfig() ItemRenderConfig {
	return ItemRenderConfig{LocalizedNames: true, ShowDescriptions: true, RenderTags: true, DarkBackground: false}
}

var (
	itemRenderMu    sync.RWMutex
	itemRenderCache *ItemRenderConfig
)

func (s *Store) ItemRenderConfig() ItemRenderConfig {
	itemRenderMu.RLock()
	if itemRenderCache != nil {
		v := *itemRenderCache
		itemRenderMu.RUnlock()
		return v
	}
	itemRenderMu.RUnlock()

	c := defaultItemRenderConfig()
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM panel_settings WHERE key = 'item_render_config'`).Scan(&raw); err == nil {
		json.Unmarshal([]byte(raw), &c)
	}
	itemRenderMu.Lock()
	itemRenderCache = &c
	itemRenderMu.Unlock()
	return c
}

func (s *Store) SetItemRenderConfig(c ItemRenderConfig) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO panel_settings (key, value) VALUES ('item_render_config', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		string(raw)); err != nil {
		return err
	}
	itemRenderMu.Lock()
	itemRenderCache = &c
	itemRenderMu.Unlock()
	return nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT username, must_change_password, role, theme, locale FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Username, &u.MustChangePassword, &u.Role, &u.Theme, &u.Locale); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CountAdmins() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&n)
	return n, err
}

func (s *Store) CreateUser(username, password, role string) error {
	if username == "" {
		return fmt.Errorf("логин не может быть пустым")
	}
	if !validRole(role) {
		return fmt.Errorf("неизвестная роль %q", role)
	}
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`, username).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return fmt.Errorf("пользователь %q уже существует", username)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users (username, password_hash, must_change_password, role) VALUES (?, ?, 1, ?)`,
		username, string(hash), role)
	return err
}

func (s *Store) DeleteUser(username string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var role string
	if err := tx.QueryRow(`SELECT role FROM users WHERE username = ?`, username).Scan(&role); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if role == RoleAdmin {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("нельзя удалить последнего администратора")
		}
	}
	if _, err := tx.Exec(`DELETE FROM users WHERE username = ?`, username); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE username = ?`, username); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SetUserRole(username, role string) error {
	if !validRole(role) {
		return fmt.Errorf("неизвестная роль %q", role)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current string
	if err := tx.QueryRow(`SELECT role FROM users WHERE username = ?`, username).Scan(&current); err != nil {
		return err
	}
	if current == RoleAdmin && role != RoleAdmin {
		var admins int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("нельзя понизить последнего администратора")
		}
	}
	if _, err := tx.Exec(`UPDATE users SET role = ? WHERE username = ?`, role, username); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) CreateSession(username string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	_, err := s.db.Exec(`INSERT INTO sessions (token, username, created_at) VALUES (?, ?, ?)`,
		token, username, time.Now().UTC().Format(time.RFC3339))
	return token, err
}

func (s *Store) Session(token string) (*User, error) {
	var username string
	err := s.db.QueryRow(`SELECT username FROM sessions WHERE token = ?`, token).Scan(&username)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	u := User{Username: username}
	err = s.db.QueryRow(`SELECT must_change_password, role, theme, locale FROM users WHERE username = ?`, username).
		Scan(&u.MustChangePassword, &u.Role, &u.Theme, &u.Locale)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

func (s *Store) SaveItemMapping(items map[string]int, gameName string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM item_mapping`); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO item_mapping (name, id) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for name, id := range items {
		if _, err := stmt.Exec(name, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO item_mapping_meta (key, value) VALUES ('synced_at', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if gameName != "" {
		if _, err := tx.Exec(`INSERT INTO item_mapping_meta (key, value) VALUES ('game_name', ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`, gameName); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ItemMapping() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT name, id FROM item_mapping`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var id int
		if err := rows.Scan(&name, &id); err != nil {
			return nil, err
		}
		out[name] = id
	}
	return out, rows.Err()
}

func (s *Store) ItemMappingCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM item_mapping`).Scan(&n)
	return n, err
}

func (s *Store) ItemMappingSyncedAt() (*time.Time, error) {
	var raw string
	err := s.db.QueryRow(`SELECT value FROM item_mapping_meta WHERE key = 'synced_at'`).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) ItemMappingSyncedGameName() (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM item_mapping_meta WHERE key = 'game_name'`).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) AdoptItemMappingGameName(gameName string) error {
	if gameName == "" {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO item_mapping_meta (key, value) VALUES ('game_name', ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, gameName)
	return err
}

func (s *Store) IconNames(scenario string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT name FROM icons WHERE scenario = ?`, scenario)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (s *Store) SaveIcon(scenario, name string, data []byte) error {
	_, err := s.db.Exec(`INSERT INTO icons (scenario, name, data, synced_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(scenario, name) DO UPDATE SET data = excluded.data, synced_at = excluded.synced_at`,
		scenario, name, data, time.Now().UTC().Format(time.RFC3339))
	return err
}

func (s *Store) Icon(scenario, name string) ([]byte, bool, error) {
	var data []byte
	err := s.db.QueryRow(`SELECT data FROM icons WHERE scenario = ? AND name = ?`, scenario, name).Scan(&data)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *Store) IconCount(scenario string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM icons WHERE scenario = ?`, scenario).Scan(&n)
	return n, err
}

type AdminPlayer struct {
	SteamId string
	Name    string
}

func (s *Store) AdminPlayers() ([]AdminPlayer, error) {
	rows, err := s.db.Query(`SELECT steam_id, name FROM admin_players ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminPlayer
	for rows.Next() {
		var p AdminPlayer
		if err := rows.Scan(&p.SteamId, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddAdminPlayer(steamID, name string) error {
	_, err := s.db.Exec(`INSERT INTO admin_players (steam_id, name) VALUES (?, ?)
		ON CONFLICT(steam_id) DO UPDATE SET name = excluded.name`, steamID, name)
	return err
}

func (s *Store) RemoveAdminPlayer(steamID string) error {
	_, err := s.db.Exec(`DELETE FROM admin_players WHERE steam_id = ?`, steamID)
	return err
}

type ModeratorPlayer struct {
	SteamId string
	Name    string
}

func (s *Store) ModeratorPlayers() ([]ModeratorPlayer, error) {
	rows, err := s.db.Query(`SELECT steam_id, name FROM moderator_players ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModeratorPlayer
	for rows.Next() {
		var p ModeratorPlayer
		if err := rows.Scan(&p.SteamId, &p.Name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) AddModeratorPlayer(steamID, name string) error {
	_, err := s.db.Exec(`INSERT INTO moderator_players (steam_id, name) VALUES (?, ?)
		ON CONFLICT(steam_id) DO UPDATE SET name = excluded.name`, steamID, name)
	return err
}

func (s *Store) RemoveModeratorPlayer(steamID string) error {
	_, err := s.db.Exec(`DELETE FROM moderator_players WHERE steam_id = ?`, steamID)
	return err
}
