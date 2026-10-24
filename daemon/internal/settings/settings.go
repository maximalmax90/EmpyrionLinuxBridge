package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	Chatbot       Chatbot       `json:"chatbot"`
	Watchdog      Watchdog      `json:"watchdog"`
	Backpack      Backpack      `json:"backpack"`
	Backup        Backup        `json:"backup"`
	Monitoring    Monitoring    `json:"monitoring"`
	Maintenance   Maintenance   `json:"maintenance"`
	Steam         Steam         `json:"steam"`
	Structures    Structures    `json:"structures"`
	Scheduler     Scheduler     `json:"scheduler"`
	Audit         Audit         `json:"audit"`
	ScenarioCache ScenarioCache `json:"scenarioCache"`
}

type ScenarioCache struct {
	Mode string `json:"mode,omitempty"`

	PublicURLBase string `json:"publicUrlBase,omitempty"`

	FTPProtocol string `json:"ftpProtocol,omitempty"`
	FTPHost     string `json:"ftpHost,omitempty"`
	FTPPort     int    `json:"ftpPort,omitempty"`
	FTPUser     string `json:"ftpUser,omitempty"`
	FTPPassword string `json:"ftpPassword,omitempty"`
	FTPKey      string `json:"ftpKey,omitempty"`
	FTPPath     string `json:"ftpPath,omitempty"`

	EmbeddedListenAddr string `json:"embeddedListenAddr,omitempty"`

	PanelListenPort int `json:"panelListenPort,omitempty"`
}

type Audit struct {
	UnauthorizedAPIRequests bool `json:"unauthorizedApiRequests,omitempty"`

	InventoryFuelAnomaly bool `json:"inventoryFuelAnomaly,omitempty"`

	WipeLeftover bool `json:"wipeLeftover,omitempty"`

	PositionJump bool `json:"positionJump,omitempty"`

	PermissionFactionChange bool `json:"permissionFactionChange,omitempty"`

	LoginTracking bool `json:"loginTracking,omitempty"`
}

func (a Audit) Enabled() bool {
	return a.UnauthorizedAPIRequests || a.InventoryFuelAnomaly || a.WipeLeftover ||
		a.PositionJump || a.PermissionFactionChange || a.LoginTracking
}

type Structures struct {
	BackupIntervalMinutes int `json:"backupIntervalMinutes"`
	MaxBackupEntries      int `json:"maxBackupEntries"`

	RequireOwnerNearby bool `json:"requireOwnerNearby"`
}

type Steam struct {
	Username string `json:"username,omitempty"`

	WebAPIKey     string `json:"webApiKey,omitempty"`
	SteamID64     string `json:"steamId64,omitempty"`
	WorkshopAppID string `json:"workshopAppId,omitempty"`

	InstalledItems []InstalledWorkshopItem `json:"installedItems,omitempty"`
}

type InstalledWorkshopItem struct {
	PublishedFileID string `json:"publishedFileId"`
	Title           string `json:"title"`
	Kind            string `json:"kind"`
	TargetDir       string `json:"targetDir"`
	TimeUpdated     int64  `json:"timeUpdated"`
}

type Maintenance struct {
	CleanOldBuildLogs bool `json:"cleanOldBuildLogs,omitempty"`

	ManualActionWarnMinutes int `json:"manualActionWarnMinutes,omitempty"`

	CheckUpdatesPeriodically bool `json:"checkUpdatesPeriodically,omitempty"`

	LastNotifiedServerBuildID       string `json:"lastNotifiedServerBuildId,omitempty"`
	LastNotifiedScenarioTimeUpdated int64  `json:"lastNotifiedScenarioTimeUpdated,omitempty"`
}

type Chatbot struct {
	WipeCooldownHours              int `json:"wipeCooldownHours"`
	VehicleTeleportCooldownMinutes int `json:"vehicleTeleportCooldownMinutes"`

	HardWipe bool `json:"hardWipe"`

	ServerSenderName string `json:"serverSenderName,omitempty"`

	CommandsEnabled map[string]bool `json:"commandsEnabled,omitempty"`

	CommandDescriptions map[string]string `json:"commandDescriptions,omitempty"`

	RulesText string `json:"rulesText,omitempty"`

	CustomCommands []CustomCommand `json:"customCommands,omitempty"`

	AdminSteamIds []string `json:"adminSteamIds,omitempty"`

	ModeratorSteamIds []string `json:"moderatorSteamIds,omitempty"`

	NotifyPlayerPresence bool `json:"notifyPlayerPresence,omitempty"`

	SurvivalCredits float64        `json:"survivalCredits"`
	SurvivalItems   []SurvivalItem `json:"survivalItems,omitempty"`

	WelcomeMessage WelcomeMessage `json:"welcomeMessage,omitempty"`
}

type WelcomeMessage struct {
	Enabled bool `json:"enabled"`

	Messages []WelcomeMessageItem `json:"messages,omitempty"`
}

type WelcomeMessageItem struct {
	DelaySeconds int    `json:"delaySeconds"`
	Text         string `json:"text"`

	ActionType string `json:"actionType,omitempty"`

	BannerPriority int `json:"bannerPriority,omitempty"`
}

type SurvivalItem struct {
	ItemID int `json:"itemId"`
	Count  int `json:"count"`
}

const (
	ActionDialog = "dialog" // ShowDialogBox — как раньше единственный вариант, модальное окно с ОК
	ActionChat   = "chat"   // обычное чат-сообщение адресату (SinglePlayer)
	ActionBanner = "banner" // экранный баннер (Request_InGameMessage_SinglePlayer), см. BannerPriority
)

type CustomCommand struct {
	Name string `json:"name"`

	Text    string `json:"text"`
	Enabled bool   `json:"enabled"`

	ActionType string `json:"actionType,omitempty"`

	BannerPriority int `json:"bannerPriority,omitempty"`

	GameDescription string `json:"gameDescription,omitempty"`

	ScriptName string `json:"scriptName,omitempty"`

	CooldownMinutes int `json:"cooldownMinutes,omitempty"`
}

type Watchdog struct {
	AutoRestartOnHang bool `json:"autoRestartOnHang"`

	LogHangs *bool `json:"logHangs,omitempty"`
}

func (w Watchdog) LogHangsEnabled() bool {
	return w.LogHangs == nil || *w.LogHangs
}

type Scheduler struct {
	Paused bool `json:"paused,omitempty"`
}

type Backpack struct {
	MinHistoryIntervalSeconds int `json:"minHistoryIntervalSeconds"`

	ForceOverwriteInventory bool `json:"forceOverwriteInventory"`

	MaxHistoryEntries int `json:"maxHistoryEntries"`
}

type Backup struct {
	Destinations [2]BackupDestination `json:"destinations"`

	Exclude BackupExclude `json:"exclude,omitempty"`
}

type BackupExclude struct {
	Elb         bool     `json:"elb,omitempty"`
	Wine        bool     `json:"wine,omitempty"`
	Saves       []string `json:"saves,omitempty"`
	ContentMode string   `json:"contentMode,omitempty"`
	Mods        []string `json:"mods,omitempty"`
	Scenarios   []string `json:"scenarios,omitempty"`
}

func (c *Config) PruneBackupExcludeName(kind, name string) bool {
	var list *[]string
	switch kind {
	case "mod":
		list = &c.Backup.Exclude.Mods
	case "scenario":
		list = &c.Backup.Exclude.Scenarios
	default:
		return false
	}
	for i, v := range *list {
		if v == name {
			*list = append((*list)[:i], (*list)[i+1:]...)
			return true
		}
	}
	return false
}

type BackupDestination struct {
	Type string `json:"type,omitempty"`

	DiskPath string `json:"diskPath,omitempty"`

	FTPProtocol string `json:"ftpProtocol,omitempty"`
	FTPHost     string `json:"ftpHost,omitempty"`
	FTPPort     int    `json:"ftpPort,omitempty"`
	FTPUser     string `json:"ftpUser,omitempty"`
	FTPPassword string `json:"ftpPassword,omitempty"`
	FTPKey      string `json:"ftpKey,omitempty"`
	FTPPath     string `json:"ftpPath,omitempty"`

	S3Endpoint  string `json:"s3Endpoint,omitempty"`
	S3Region    string `json:"s3Region,omitempty"`
	S3Bucket    string `json:"s3Bucket,omitempty"`
	S3AccessKey string `json:"s3AccessKey,omitempty"`
	S3SecretKey string `json:"s3SecretKey,omitempty"`

	Retention Retention `json:"retention,omitempty"`
}

type Retention struct {
	Last    int `json:"last,omitempty"`
	Hourly  int `json:"hourly,omitempty"`
	Daily   int `json:"daily,omitempty"`
	Weekly  int `json:"weekly,omitempty"`
	Monthly int `json:"monthly,omitempty"`
	Yearly  int `json:"yearly,omitempty"`
}

func (r Retention) Off() bool {
	return r.Last == 0 && r.Hourly == 0 && r.Daily == 0 && r.Weekly == 0 && r.Monthly == 0 && r.Yearly == 0
}

type Monitoring struct {
	EnabledMounts []string `json:"enabledMounts"`

	WarningPercent  int `json:"warningPercent"`
	CriticalPercent int `json:"criticalPercent"`

	HistoryRetentionDays int `json:"historyRetentionDays,omitempty"`
}

func Default() Config {
	return Config{
		Chatbot:  Chatbot{WipeCooldownHours: 24, VehicleTeleportCooldownMinutes: 10, HardWipe: false, ServerSenderName: "Сервер"},
		Watchdog: Watchdog{AutoRestartOnHang: false},
		Backpack: Backpack{MinHistoryIntervalSeconds: 60, MaxHistoryEntries: 300},

		Backup:     Backup{Destinations: [2]BackupDestination{{Type: "snapshot"}, {}}},
		Monitoring: Monitoring{WarningPercent: 60, CriticalPercent: 85},

		Structures: Structures{BackupIntervalMinutes: 60, MaxBackupEntries: 3, RequireOwnerNearby: true},
	}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	c := Default()
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	c.Backup.Destinations[0].Type = "snapshot"
	return c, nil
}

func Save(path string, c Config) error {

	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
