using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Net;
using System.Text;
using System.Text.Json;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;
using Eleon;
using Eleon.Modding;

namespace ElbMod
{
    public class ElbMod : IMod, ModInterface
    {
        const bool Debug = true;

        const int Port = 31914;
        const int MaxRecentChat = 200;
        static readonly TimeSpan LegacyTimeout = TimeSpan.FromSeconds(10);

        IModApi modApi;
        ModGameAPI legacyApi;
        HttpListener listener;
        Thread listenerThread;

        readonly object chatLock = new object();
        readonly List<ChatEntry> recentChat = new List<ChatEntry>();

        readonly object structureBackupQueueLock = new object();
        readonly List<JsonElement> structureBackupQueue = new List<JsonElement>();

        const string ItemMappingFileName = "ItemMapping.json";

        volatile IPlayfield pfServerPlayfield;
        DateTime lastCommandCheckUtc = DateTime.MinValue;
        static readonly TimeSpan CommandCheckInterval = TimeSpan.FromMilliseconds(100);
        readonly Dictionary<int, DateTime> lastStructureBackupUtc = new Dictionary<int, DateTime>();
        DateTime lastStructureBackupCheckUtc = DateTime.MinValue;
        static readonly TimeSpan StructureBackupCheckInterval = TimeSpan.FromMinutes(5);

        readonly ConcurrentDictionary<ushort, TaskCompletionSource<object>> pending =
            new ConcurrentDictionary<ushort, TaskCompletionSource<object>>();
        int nextSeq = new Random().Next(10000);

        class ChatEntry
        {
            public string sender { get; set; }
            public string steamId { get; set; }
            public string text { get; set; }
            public string timestamp { get; set; }
            public string channel { get; set; }
            public string target { get; set; }
            public int factionId { get; set; }
            public ulong gameTime { get; set; }
        }


        class ChatbotConfig
        {
            public int WipeCooldownHours = 24;
            public int VehicleTeleportCooldownMinutes = 10;
            public bool HardWipe = false;
            public Dictionary<string, bool> CommandsEnabled = new Dictionary<string, bool>();
            public Dictionary<string, string> CommandDescriptions = new Dictionary<string, string>();
            public List<CustomCommandConfig> CustomCommands = new List<CustomCommandConfig>();
            public HashSet<string> AdminSteamIds = new HashSet<string>();
            public double SurvivalCredits = 0;
            public List<SurvivalItemConfig> SurvivalItems = new List<SurvivalItemConfig>();
            public string RulesText = "";
        }
        readonly ChatbotConfig chatbotConfig = new ChatbotConfig();

        class SurvivalItemConfig
        {
            public int ItemId;
            public int Count;
        }

        static readonly HashSet<string> BuiltinCommands = new HashSet<string> { "?", "help", "wipe", "get", "ship", "destroy", "restart", "stop", "time", "survival", "rules" };

        const double VehicleActionRadiusMeters = 200;

        static double Distance(PVector3 a, PVector3 b)
        {
            var dx = a.x - b.x;
            var dy = a.y - b.y;
            var dz = a.z - b.z;
            return Math.Sqrt(dx * dx + dy * dy + dz * dz);
        }

        bool IsCommandEnabled(string cmd) =>
            !chatbotConfig.CommandsEnabled.TryGetValue(cmd, out var enabled) || enabled;

        const string ActionDialog = "dialog";
        const string ActionChat = "chat";
        const string ActionBanner = "banner";

        class CustomCommandConfig
        {
            public string Name;
            public string Text;
            public bool Enabled;
            public string ActionType;
            public int BannerPriority;
            public string GameDescription;
            public string ScriptName;
            public int CooldownMinutes;
        }

        string CustomCommandText(CustomCommandConfig c) => c.Text ?? "";

        void DeliverText(int entityId, string actionType, string text, int bannerPriority)
        {
            switch (actionType)
            {
                case ActionChat:
                    SendChatToPlayer(entityId, text);
                    break;
                case ActionBanner:
                    SendBannerToPlayer(entityId, text, bannerPriority);
                    break;
                default:
                    ReplyToPlayer(entityId, text);
                    break;
            }
        }

        void DeliverCustomCommand(int entityId, string steamId, string playerName, CustomCommandConfig c, string arg)
        {
            if (!string.IsNullOrEmpty(c.ScriptName))
            {
                QueueScriptRun(steamId, playerName, c.ScriptName, arg, c.ActionType, c.BannerPriority);
                return;
            }
            DeliverText(entityId, c.ActionType, CustomCommandText(c), c.BannerPriority);
        }

        void QueueScriptRun(string steamId, string playerName, string scriptName, string arg, string actionType, int bannerPriority)
        {
            try
            {
                var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "script-queue");
                Directory.CreateDirectory(dir);
                var file = Path.Combine(dir, $"{steamId}_{DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()}.json");
                File.WriteAllText(file, JsonSerializer.Serialize(new
                {
                    steamId,
                    playerName,
                    scriptName,
                    arg = arg ?? "",
                    actionType,
                    bannerPriority,
                }));
                if (Debug) modApi.Log($"ElbMod: script {scriptName} run request for {playerName} ({steamId}) recorded to {file}");
            }
            catch (Exception ex) { modApi.LogError("ElbMod: QueueScriptRun: " + ex); }
        }

        void SendChatToPlayer(int entityId, string text)
        {
            try
            {
                var msg = new MessageData
                {
                    Text = text,
                    SenderType = global::Eleon.SenderType.ServerInfo,
                    SenderNameOverride = "bot",
                    Channel = global::Eleon.MsgChannel.SinglePlayer,
                    RecipientEntityId = entityId,
                };
                modApi.Application.SendChatMessage(msg);
            }
            catch (Exception ex) { modApi.LogError("ElbMod: SendChatToPlayer: " + ex); }
        }

        void SendBannerToPlayer(int entityId, string text, int priority)
        {
            try
            {
                LegacyRequest(CmdId.Request_InGameMessage_SinglePlayer,
                    new IdMsgPrio(entityId, text ?? "", (byte)priority, 3f), waitForResponse: false);
            }
            catch (Exception ex) { modApi.LogError("ElbMod: SendBannerToPlayer: " + ex); }
        }

        const string ChatbotSettingsPath = @"Z:\server\EMP\elb\settings.json";

        class ChatbotSettingsJson
        {
            public int wipeCooldownHours { get; set; }
            public int vehicleTeleportCooldownMinutes { get; set; }
            public bool hardWipe { get; set; }
            public Dictionary<string, bool> commandsEnabled { get; set; }
            public Dictionary<string, string> commandDescriptions { get; set; }
            public string rulesText { get; set; }
            public List<CustomCommandJson> customCommands { get; set; }
            public List<string> adminSteamIds { get; set; }
            public double survivalCredits { get; set; }
            public List<SurvivalItemJson> survivalItems { get; set; }
        }
        class CustomCommandJson
        {
            public string name { get; set; }
            public string text { get; set; }
            public bool enabled { get; set; }
            public string actionType { get; set; }
            public int bannerPriority { get; set; }
            public string gameDescription { get; set; }
            public string scriptName { get; set; }
            public int cooldownMinutes { get; set; }
        }
        class SurvivalItemJson
        {
            public int itemId { get; set; }
            public int count { get; set; }
        }
        class SettingsFileJson
        {
            public ChatbotSettingsJson chatbot { get; set; }
            public StructuresSettingsJson structures { get; set; }
        }
        class StructuresSettingsJson
        {
            public int? backupIntervalMinutes { get; set; }
            public bool? requireOwnerNearby { get; set; }
        }

        void ReloadChatbotConfig()
        {
            try
            {
                if (!File.Exists(ChatbotSettingsPath)) return;
                var parsed = JsonSerializer.Deserialize<SettingsFileJson>(File.ReadAllText(ChatbotSettingsPath))?.chatbot;
                if (parsed == null) return;
                chatbotConfig.WipeCooldownHours = parsed.wipeCooldownHours;
                chatbotConfig.VehicleTeleportCooldownMinutes = parsed.vehicleTeleportCooldownMinutes;
                chatbotConfig.HardWipe = parsed.hardWipe;
                chatbotConfig.CommandsEnabled = parsed.commandsEnabled ?? new Dictionary<string, bool>();
                chatbotConfig.CommandDescriptions = parsed.commandDescriptions ?? new Dictionary<string, string>();
                chatbotConfig.RulesText = parsed.rulesText ?? "";
                chatbotConfig.CustomCommands = (parsed.customCommands ?? new List<CustomCommandJson>())
                    .Select(c => new CustomCommandConfig
                    {
                        Name = (c.name ?? "").Trim().ToLowerInvariant(),
                        Text = c.text,
                        Enabled = c.enabled,
                        ActionType = string.IsNullOrEmpty(c.actionType) ? ActionDialog : c.actionType,
                        BannerPriority = c.bannerPriority,
                        GameDescription = c.gameDescription,
                        ScriptName = c.scriptName,
                        CooldownMinutes = c.cooldownMinutes,
                    })
                    .Where(c => !string.IsNullOrEmpty(c.Name) && !BuiltinCommands.Contains(c.Name))
                    .ToList();
                chatbotConfig.AdminSteamIds = new HashSet<string>(parsed.adminSteamIds ?? new List<string>());
                chatbotConfig.SurvivalCredits = parsed.survivalCredits;
                chatbotConfig.SurvivalItems = (parsed.survivalItems ?? new List<SurvivalItemJson>())
                    .Select(i => new SurvivalItemConfig { ItemId = i.itemId, Count = i.count })
                    .ToList();
            }
            catch (Exception ex) { modApi.LogError("ElbMod: ReloadChatbotConfig: " + ex); }
        }

        class Translations
        {
            Dictionary<string, string> current = new Dictionary<string, string>();
            Dictionary<string, string> fallbackEn = new Dictionary<string, string>();
            readonly Action<string> logError;

            public Translations(Action<string> logError) { this.logError = logError; }

            string Dir => Path.GetDirectoryName(System.Reflection.Assembly.GetExecutingAssembly().Location);

            public void SetLocale(string locale)
            {
                fallbackEn = LoadFile("en");
                current = string.Equals(locale, "en", StringComparison.OrdinalIgnoreCase)
                    ? fallbackEn
                    : LoadFile(string.IsNullOrWhiteSpace(locale) ? "ru" : locale);
            }

            Dictionary<string, string> LoadFile(string locale)
            {
                try
                {
                    var file = Path.Combine(Dir, "Translations", $"cb.{locale}.json");
                    if (!File.Exists(file)) return new Dictionary<string, string>();
                    return JsonSerializer.Deserialize<Dictionary<string, string>>(File.ReadAllText(file))
                           ?? new Dictionary<string, string>();
                }
                catch (Exception ex)
                {
                    logError($"Translations: failed to read {locale}: {ex}");
                    return new Dictionary<string, string>();
                }
            }

            public string T(string key, params object[] args)
            {
                if (!current.TryGetValue(key, out var fmt) && !fallbackEn.TryGetValue(key, out fmt))
                    return key;
                return args.Length > 0 ? string.Format(fmt, args) : fmt;
            }
        }
        Translations tr;

        class ChatbotCooldowns
        {
            public Dictionary<string, DateTime> LastWipeUtc { get; set; } = new Dictionary<string, DateTime>();
            public Dictionary<string, DateTime> LastTeleportUtc { get; set; } = new Dictionary<string, DateTime>();
            public Dictionary<string, DateTime> LastCustomCommandUtc { get; set; } = new Dictionary<string, DateTime>();
            public HashSet<string> SurvivalClaimed { get; set; } = new HashSet<string>();
        }
        readonly object chatbotLock = new object();
        ChatbotCooldowns cooldowns = new ChatbotCooldowns();


        public void Init(IModApi modApi)
        {
            this.modApi = modApi;
            if (Debug) modApi.Log($"ElbMod: Init called, Mode={modApi.Application.Mode}");

            if (modApi.Application.Mode != ApplicationMode.DedicatedServer)
            {
                modApi.Application.GameEntered += hasEntered =>
                {
                    if (Debug) modApi.Log($"ElbMod (PfServer): GameEntered({hasEntered}) fired");
                    if (hasEntered) TryWriteItemMapping();
                };
                modApi.Application.OnPlayfieldLoaded += pf =>
                {
                    if (Debug) modApi.Log($"ElbMod (PfServer): OnPlayfieldLoaded fired, playfield={pf?.Name}");
                    pfServerPlayfield = pf;
                    TryWriteItemMapping();
                    if (!string.IsNullOrEmpty(pf?.Name)) Task.Run(() => ProcessPendingHardWipeDestroys(pf.Name));
                };
                modApi.Application.OnPlayfieldUnloading += pf =>
                {
                    if (Debug) modApi.Log($"ElbMod (PfServer): OnPlayfieldUnloading, playfield={pf?.Name}");
                    if (ReferenceEquals(pf, pfServerPlayfield)) pfServerPlayfield = null;
                };
                modApi.Application.Update += OnPfServerUpdate;
                if (Debug) modApi.Log("ElbMod (PfServer): subscribed to GameEntered/OnPlayfieldLoaded/OnPlayfieldUnloading/Update");
                return;
            }

            modApi.Application.ChatMessageSent += OnChatMessageSent;
            tr = new Translations(msg => modApi.LogError("ElbMod: " + msg));
            tr.SetLocale("ru");
            ReloadChatbotConfig();
            if (Debug) modApi.Log($"ElbMod: chatbot config at startup: wipe={chatbotConfig.WipeCooldownHours}h, getship={chatbotConfig.VehicleTeleportCooldownMinutes}min (from {ChatbotSettingsPath}, exists={File.Exists(ChatbotSettingsPath)})");
            LoadCooldowns();

            listener = new HttpListener();
            listener.Prefixes.Add($"http://127.0.0.1:{Port}/");
            listener.Start();

            listenerThread = new Thread(ListenLoop) { IsBackground = true };
            listenerThread.Start();

            modApi.Log($"ElbMod: initialized, HTTP API on http://127.0.0.1:{Port}/");
        }

        public void Shutdown()
        {
            try { listener?.Stop(); } catch {  }
            modApi.Log("ElbMod: shutdown");
        }

        bool itemMappingWritten;

        void TryWriteItemMapping()
        {
            if (itemMappingWritten) return;
            try
            {
                if (Debug) modApi.Log("ElbMod (PfServer): TryWriteItemMapping: start");
                var map = modApi.Application.GetBlockAndItemMapping();
                if (Debug) modApi.Log($"ElbMod (PfServer): GetBlockAndItemMapping -> {(map == null ? "null" : $"Count={map.Count}")}");
                if (map == null || map.Count == 0) return;

                var saveGamePath = modApi.Application.GetPathFor(AppFolder.SaveGame);
                if (Debug) modApi.Log($"ElbMod (PfServer): SaveGame path = {saveGamePath ?? "null"}");
                var dir = Path.Combine(saveGamePath, "Mods", "ElbMod");
                Directory.CreateDirectory(dir);
                var file = Path.Combine(dir, ItemMappingFileName);
                File.WriteAllText(file, JsonSerializer.Serialize(map));
                itemMappingWritten = true;
                if (Debug) modApi.Log($"ElbMod (PfServer): item mapping written to {file} ({map.Count} entries)");
            }
            catch (Exception ex) { modApi.LogError("ElbMod (PfServer): TryWriteItemMapping: " + ex); }
        }

        void OnPfServerUpdate()
        {
            var pf = pfServerPlayfield;
            if (pf == null) return;

            if ((DateTime.UtcNow - lastCommandCheckUtc) >= CommandCheckInterval)
            {
                lastCommandCheckUtc = DateTime.UtcNow;
                try { ProcessPendingStructureCommands(pf); }
                catch (Exception ex) { modApi.LogError("ElbMod (PfServer): ProcessPendingStructureCommands: " + ex); }
            }

            if ((DateTime.UtcNow - lastStructureBackupCheckUtc) >= StructureBackupCheckInterval)
            {
                lastStructureBackupCheckUtc = DateTime.UtcNow;
                try
                {
                    var (minutes, requireOwnerNearby) = ReadStructureBackupSettings();
                    var due = CollectDueStructureBackups(pf, TimeSpan.FromMinutes(minutes), requireOwnerNearby);
                    if (due.Count > 0)
                    {
                        var pfName = pf.Name;
                        Task.Run(() =>
                        {
                            try { RunStructureBackups(pfName, due); }
                            catch (Exception ex) { modApi.LogError("ElbMod (PfServer): periodic structure backup, sending: " + ex); }
                        });
                    }
                }
                catch (Exception ex) { modApi.LogError("ElbMod (PfServer): periodic structure backup: " + ex); }
            }
        }

        (int minutes, bool requireOwnerNearby) ReadStructureBackupSettings()
        {
            try
            {
                if (!File.Exists(ChatbotSettingsPath)) return (60, true);
                var s = JsonSerializer.Deserialize<SettingsFileJson>(File.ReadAllText(ChatbotSettingsPath))?.structures;
                var minutes = s?.backupIntervalMinutes ?? 60;
                return (minutes > 0 ? minutes : 60, s?.requireOwnerNearby ?? true);
            }
            catch (Exception ex) { modApi.LogError("ElbMod: ReadStructureBackupSettings: " + ex); return (60, true); }
        }

        static readonly DeviceTypeName[] ContainerDeviceTypes = {
            DeviceTypeName.Container, DeviceTypeName.AmmoCntr,
            DeviceTypeName.Fridge, DeviceTypeName.HarvestCntr,
        };

        static readonly DeviceTypeName[] AnchorDeviceTypes = {
            DeviceTypeName.Container, DeviceTypeName.AmmoCntr, DeviceTypeName.Fridge, DeviceTypeName.HarvestCntr,
            DeviceTypeName.Light, DeviceTypeName.LCD, DeviceTypeName.Portal, DeviceTypeName.Teleporter,
        };

        List<object> CaptureGeometryBlocks(IStructure structure)
        {
            var min = structure.MinPos; var max = structure.MaxPos;
            VectorInt3? anchor = null;
            foreach (var dt in AnchorDeviceTypes)
            {
                IDevicePosList posList;
                try { posList = structure.GetDevices(dt); } catch { continue; }
                if (posList == null || posList.Count == 0) continue;
                anchor = posList.GetAt(0);
                break;
            }
            if (anchor == null) return null;

            var a = anchor.Value;
            int dx = (max.x - min.x) + 4, dy = (max.y - min.y) + 4, dz = (max.z - min.z) + 4;
            var blocks = new List<object>();
            for (int x = a.x - dx; x <= a.x + dx; x++)
                for (int y = a.y - dy; y <= a.y + dy; y++)
                    for (int z = a.z - dz; z <= a.z + dz; z++)
                    {
                        IBlock b;
                        try { b = structure.GetBlock(x, y, z); } catch { continue; }
                        if (b == null) continue;
                        b.Get(out int t, out int sh, out int rot, out bool act);
                        if (t == 0) continue;
                        blocks.Add(new { x, y, z, type = t, shape = sh, rot, active = act });
                    }
            return blocks;
        }

        class StructureBackupTarget
        {
            public int Id;
            public string Playfield;
            public string Name;
            public string TypeName;
            public float PosX, PosY, PosZ;
            public byte FactionGroup;
            public int FactionId;
            public List<object> Containers;
            public object FuelTank, OxygenTank, PentaxidTank;
            public List<object> Geometry;
            public int GeometryBlockCount;
        }

        const float NearbyMaxDistance = 300f;

        bool IsOwnerNearby(IPlayfield pf, FactionData faction, UnityEngine.Vector3 structurePos)
        {
            foreach (var kv in pf.Players)
            {
                var player = kv.Value;
                if (player == null) continue;
                bool owns = faction.Group == FactionGroup.Player
                    ? player.Id == faction.Id
                    : faction.Group == FactionGroup.Faction && player.Faction.Group == FactionGroup.Faction && player.Faction.Id == faction.Id;
                if (!owns) continue;
                if (UnityEngine.Vector3.Distance(player.Position, structurePos) <= NearbyMaxDistance) return true;
            }
            return false;
        }

        List<StructureBackupTarget> CollectDueStructureBackups(IPlayfield pf, TimeSpan interval, bool requireOwnerNearby)
        {
            var now = DateTime.UtcNow;
            var due = new List<StructureBackupTarget>();

            foreach (var kv in pf.Entities)
            {
                IStructure structure;
                try { structure = kv.Value?.Structure; } catch { structure = null; }
                if (structure == null) continue;

                var entityId = kv.Key;

                if (!kv.Value.IsLocal) continue;

                var faction = kv.Value.Faction;
                if (faction.Group != FactionGroup.Player && !(faction.Group == FactionGroup.Faction && faction.Id > 0)) continue;

                if (lastStructureBackupUtc.TryGetValue(entityId, out var last) && (now - last) < interval) continue;

                if (requireOwnerNearby)
                {
                    bool nearby;
                    try { nearby = IsOwnerNearby(pf, faction, kv.Value.Position); } catch { nearby = false; }
                    if (!nearby) continue;
                }

                lastStructureBackupUtc[entityId] = now;

                due.Add(CollectStructureBackupTarget(entityId, pf.Name, kv.Value, structure));
            }
            return due;
        }

        StructureBackupTarget CollectStructureBackupTarget(int entityId, string playfieldName, IEntity entity, IStructure structure)
        {
                var containers = new List<object>();
                var seenPositions = new HashSet<(int, int, int)>();

                foreach (var deviceType in ContainerDeviceTypes)
                {
                    IDevicePosList posList;
                    try { posList = structure.GetDevices(deviceType); }
                    catch (Exception ex)
                    {
                        modApi.LogError($"ElbMod (PfServer): GetDevices({entityId}, {deviceType}): " + ex);
                        continue;
                    }
                    if (posList == null) continue;

                    for (int i = 0; i < posList.Count; i++)
                    {
                        var pos = posList.GetAt(i);
                        if (!seenPositions.Add((pos.x, pos.y, pos.z))) continue;

                        IContainer container;
                        try { container = structure.GetDevice<IContainer>(pos); }
                        catch { container = null; }
                        if (container == null) continue;

                        string name;
                        try { name = structure.GetBlock(pos)?.CustomName; } catch { name = null; }

                        List<ItemStack> content;
                        try { content = container.GetContent(); }
                        catch (Exception ex)
                        {
                            modApi.LogError($"ElbMod (PfServer): GetContent({entityId}/{pos.x},{pos.y},{pos.z}): " + ex);
                            continue;
                        }

                        containers.Add(new
                        {
                            name,
                            deviceType = deviceType.ToString(),
                            position = new { x = pos.x, y = pos.y, z = pos.z },
                            items = (content ?? new List<ItemStack>())
                                .Select(i => new { id = i.id, count = i.count, slotIdx = i.slotIdx, ammo = i.ammo, decay = i.decay })
                                .ToList(),
                        });
                    }
                }

                object Tank(IStructureTank t) => t == null ? null :
                    new { capacity = t.Capacity, content = t.Content, usesIntegerAmounts = t.UsesIntegerAmounts };

                List<object> geometry = null;
                try { geometry = CaptureGeometryBlocks(structure); }
                catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): CaptureGeometryBlocks({entityId}): " + ex); }

                return new StructureBackupTarget
                {
                    Id = entityId,
                    Playfield = playfieldName,
                    Name = entity.Name,
                    TypeName = entity.Type.ToString(),
                    PosX = entity.Position.x,
                    PosY = entity.Position.y,
                    PosZ = entity.Position.z,
                    FactionGroup = (byte)entity.Faction.Group,
                    FactionId = entity.Faction.Id,
                    Containers = containers,
                    FuelTank = Tank(structure.FuelTank),
                    OxygenTank = Tank(structure.OxygenTank),
                    PentaxidTank = Tank(structure.PentaxidTank),
                    Geometry = geometry,
                    GeometryBlockCount = structure.BlockCount,
                };
        }

        void RunStructureBackups(string playfield, List<StructureBackupTarget> targets)
        {
            foreach (var t in targets)
            {
                try
                {
                    var payload = new
                    {
                        id = t.Id,
                        playfield = t.Playfield,
                        typeName = t.TypeName,
                        posX = t.PosX,
                        posY = t.PosY,
                        posZ = t.PosZ,
                        factionGroup = t.FactionGroup,
                        factionId = t.FactionId,
                        name = t.Name,
                        containers = t.Containers,
                        fuelTank = t.FuelTank,
                        oxygenTank = t.OxygenTank,
                        pentaxidTank = t.PentaxidTank,
                        geometryBlocks = t.Geometry,
                        geometryBlockCount = t.GeometryBlockCount,
                    };
                    PushStructureBackup(payload);
                }
                catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): structure backup {t.Id}: " + ex); }
            }
        }

        void PushStructureBackup(object payload)
        {
            using (var client = new WebClient())
            {
                client.Headers[HttpRequestHeader.ContentType] = "application/json";
                client.UploadString("http://127.0.0.1:31914/structures/backup/push", JsonSerializer.Serialize(payload));
            }
            if (Debug) modApi.Log("ElbMod (PfServer): structure backup sent to the Dedi queue");
        }

        void ProcessPendingStructureCommands(IPlayfield pf)
        {
            var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "structure-commands-pending");
            var file = Path.Combine(dir, SanitizeForFilename(pf.Name) + ".json");
            if (!File.Exists(file)) return;

            PendingStructureCommandsFile pending;
            try { pending = JsonSerializer.Deserialize<PendingStructureCommandsFile>(File.ReadAllText(file)); }
            catch (Exception ex)
            {
                modApi.LogError("ElbMod (PfServer): ProcessPendingStructureCommands parsing file: " + ex);
                try { File.Delete(file); } catch {  }
                return;
            }
            if (pending?.commands == null || pending.commands.Count == 0) { try { File.Delete(file); } catch { } return; }

            void Fill(IStructureTank tank, float? percent)
            {
                if (tank == null || percent == null) return;
                var target = tank.Capacity * (percent.Value / 100f);
                var delta = target - tank.Content;
                if (delta > 0) tank.AddContent(delta);
            }

            foreach (var cmd in pending.commands)
            {
                IStructure structure = null;
                IEntity entity = null;
                foreach (var kv in pf.Entities)
                {
                    if (kv.Key != cmd.id) continue;
                    entity = kv.Value;
                    try { structure = kv.Value?.Structure; } catch { structure = null; }
                    break;
                }
                if (structure == null)
                {
                    if (Debug) modApi.Log($"ElbMod (PfServer): refuel of structure {cmd.id} — structure not found on {pf.Name}");
                    continue;
                }

                if (cmd.restoreGeometry != null && cmd.restoreGeometry.Count > 0)
                {
                    try
                    {
                        int written = 0, failed = 0;
                        var expected = new HashSet<(int, int, int)>();
                        foreach (var b in cmd.restoreGeometry)
                        {
                            expected.Add((b.x, b.y, b.z));
                            try
                            {
                                var block = structure.GetBlock(b.x, b.y, b.z);
                                if (block == null) { failed++; continue; }
                                block.Set(b.type, b.shape, b.rot, b.active);
                                written++;
                            }
                            catch { failed++; }
                        }
                        modApi.Log($"ElbMod (PfServer): [restoregeometry] id={cmd.id} wrote {written}/{cmd.restoreGeometry.Count} blocks, errors={failed}");

                        try
                        {
                            var min = structure.MinPos; var max = structure.MaxPos;
                            VectorInt3? anchor = null;
                            foreach (var dt in AnchorDeviceTypes)
                            {
                                IDevicePosList posList;
                                try { posList = structure.GetDevices(dt); } catch { continue; }
                                if (posList == null || posList.Count == 0) continue;
                                anchor = posList.GetAt(0);
                                break;
                            }
                            if (anchor != null)
                            {
                                var a = anchor.Value;
                                int dx = (max.x - min.x) + 4, dy = (max.y - min.y) + 4, dz = (max.z - min.z) + 4;
                                int cleared = 0;
                                for (int x = a.x - dx; x <= a.x + dx; x++)
                                    for (int y = a.y - dy; y <= a.y + dy; y++)
                                        for (int z = a.z - dz; z <= a.z + dz; z++)
                                        {
                                            if (expected.Contains((x, y, z))) continue;
                                            IBlock b2;
                                            try { b2 = structure.GetBlock(x, y, z); } catch { continue; }
                                            if (b2 == null) continue;
                                            b2.Get(out int t, out int sh, out int rot, out bool act);
                                            if (t == 0) continue;
                                            try { b2.Set(0, 0, 0, false); cleared++; } catch { }
                                        }
                                if (cleared > 0) modApi.Log($"ElbMod (PfServer): [restoregeometry] id={cmd.id} cleared phantom cells: {cleared}");
                            }
                        }
                        catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): [restoregeometry] clearing phantoms {cmd.id}: " + ex); }
                    }
                    catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): [restoregeometry] id={cmd.id}: " + ex); }
                }

                try
                {
                    Fill(structure.FuelTank, cmd.fuelPercent);
                    Fill(structure.OxygenTank, cmd.oxygenPercent);
                    Fill(structure.PentaxidTank, cmd.pentaxidPercent);
                    if (Debug) modApi.Log($"ElbMod (PfServer): refuel of structure {cmd.id} applied (fuel={cmd.fuelPercent}, oxygen={cmd.oxygenPercent}, pentaxid={cmd.pentaxidPercent})");
                }
                catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): refuel of structure {cmd.id}: " + ex); }

                if (cmd.restoreContainers != null && cmd.restoreContainers.Count > 0)
                {
                    int restored = 0;
                    foreach (var c in cmd.restoreContainers)
                    {
                        try
                        {
                            var container = structure.GetDevice<IContainer>(c.x, c.y, c.z);
                            if (container == null)
                            {
                                modApi.LogError($"ElbMod (PfServer): restore containers {cmd.id}: container at {c.x},{c.y},{c.z} not found");
                                continue;
                            }
                            var items = (c.items ?? new List<ItemStackCommand>())
                                .Select(i => new ItemStack(i.id, i.count) { slotIdx = (byte)i.slotIdx, ammo = i.ammo, decay = i.decay })
                                .ToList();
                            container.SetContent(items);
                            restored++;
                        }
                        catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): restore container {cmd.id} @ {c.x},{c.y},{c.z}: " + ex); }
                    }
                    if (Debug) modApi.Log($"ElbMod (PfServer): restored {restored}/{cmd.restoreContainers.Count} containers of structure {cmd.id}");
                }

                if (cmd.forceBackupNow == true)
                {
                    try
                    {
                        var target = CollectStructureBackupTarget(cmd.id, pf.Name, entity, structure);
                        var pfName = pf.Name;
                        Task.Run(() =>
                        {
                            try { RunStructureBackups(pfName, new List<StructureBackupTarget> { target }); }
                            catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): [forcebackup] sending {cmd.id}: " + ex); }
                        });
                        if (Debug) modApi.Log($"ElbMod (PfServer): [forcebackup] id={cmd.id} snapshot built, send queued in background");
                    }
                    catch (Exception ex) { modApi.LogError($"ElbMod (PfServer): [forcebackup] id={cmd.id}: " + ex); }
                }
            }

            try { File.Delete(file); } catch (Exception ex) { modApi.LogError("ElbMod (PfServer): deleting processed command queue: " + ex); }
        }

        void EnqueueStructureCommand(string playfield, int id, Action<StructureCommand> mutate)
        {
            var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "structure-commands-pending");
            Directory.CreateDirectory(dir);
            var file = Path.Combine(dir, SanitizeForFilename(playfield) + ".json");
            List<StructureCommand> commands = new List<StructureCommand>();
            if (File.Exists(file))
            {
                try { commands = JsonSerializer.Deserialize<PendingStructureCommandsFile>(File.ReadAllText(file))?.commands ?? new List<StructureCommand>(); }
                catch (Exception ex) { modApi.LogError("ElbMod: EnqueueStructureCommand reading queue: " + ex); }
            }
            var cmd = commands.Find(c => c.id == id);
            if (cmd == null)
            {
                cmd = new StructureCommand { id = id };
                commands.Add(cmd);
            }
            mutate(cmd);
            File.WriteAllText(file, JsonSerializer.Serialize(new PendingStructureCommandsFile { playfield = playfield, commands = commands }));
        }

        void OnChatMessageSent(MessageData msg)
        {
            var sender = msg.SenderNameOverride;
            string steamId = null;
            if (string.IsNullOrEmpty(sender) && msg.SenderType == global::Eleon.SenderType.Player)
            {
                var pd = modApi.Application.GetPlayerDataFor(msg.SenderEntityId);
                if (pd.HasValue)
                {
                    sender = pd.Value.PlayerName;
                    steamId = pd.Value.SteamId;
                }
            }
            if (string.IsNullOrEmpty(sender))
                sender = msg.SenderEntityId.ToString();

            string target = null;
            int factionId = 0;
            if (msg.Channel == global::Eleon.MsgChannel.SinglePlayer && msg.RecipientEntityId >= 0)
            {
                var rpd = modApi.Application.GetPlayerDataFor(msg.RecipientEntityId);
                if (rpd.HasValue) target = rpd.Value.PlayerName;
            }
            else if (msg.Channel == global::Eleon.MsgChannel.Faction)
            {
                if (msg.SenderFaction.Id > 0) factionId = msg.SenderFaction.Id;
            }

            lock (chatLock)
            {
                recentChat.Add(new ChatEntry
                {
                    sender = sender,
                    steamId = steamId,
                    text = msg.Text,
                    gameTime = msg.GameTime,
                    factionId = factionId,
                    timestamp = DateTimeOffset.UtcNow.ToString("o"),
                    channel = msg.Channel.ToString().ToLowerInvariant(),
                    target = target,
                });
                if (recentChat.Count > MaxRecentChat)
                    recentChat.RemoveAt(0);
            }

            if (msg.SenderType == global::Eleon.SenderType.Player &&
                msg.Channel == global::Eleon.MsgChannel.Server &&
                !string.IsNullOrEmpty(msg.Text) &&
                msg.Text.TrimStart().StartsWith("cb:", StringComparison.OrdinalIgnoreCase))
            {
                Task.Run(() => HandleChatbotCommand(msg));
            }
        }

        async Task HandleChatbotCommand(MessageData msg)
        {
            try
            {
                ReloadChatbotConfig();

                var pd = modApi.Application.GetPlayerDataFor(msg.SenderEntityId);
                if (!pd.HasValue) return;
                var steamId = pd.Value.SteamId;
                var playerName = pd.Value.PlayerName;
                var entityId = msg.SenderEntityId;

                var rest = msg.Text.Trim().Substring(3); 
                var parts = rest.Split(':');
                var cmd = parts.Length > 0 ? parts[0].Trim().ToLowerInvariant() : "";

                if (BuiltinCommands.Contains(cmd) && !IsCommandEnabled(cmd))
                {
                    ReplyToPlayer(entityId, tr.T("command_disabled"));
                    return;
                }

                switch (cmd)
                {
                    case "?":
                    case "help":
                        ReplyToPlayer(entityId, BuildHelpText(steamId));
                        break;

                    case "wipe":
                        HandleWipeRequest(entityId, steamId, playerName);
                        break;

                    case "get":
                        HandleGetList(entityId, pd.Value.PlayfieldName);
                        break;

                    case "ship":
                        {
                            int? requestedId = null;
                            if (parts.Length > 1 && int.TryParse(parts[1].Trim(), out var shipId)) requestedId = shipId;
                            HandleShipRequest(entityId, steamId, pd.Value.PlayfieldName, requestedId);
                        }
                        break;

                    case "destroy":
                        {
                            if (parts.Length > 1 && int.TryParse(parts[1].Trim(), out var destroyId))
                                HandleDestroyRequest(entityId, pd.Value.PlayfieldName, destroyId);
                            else
                                ReplyToPlayer(entityId, tr.T("destroy_need_id"));
                        }
                        break;

                    case "time":
                        ReplyToPlayer(entityId, tr.T("time_now", DateTime.Now.ToString("dd.MM.yyyy HH:mm")));
                        break;

                    case "survival":
                        HandleSurvivalRequest(entityId, steamId);
                        break;

                    case "rules":
                        ReplyToPlayer(entityId, RulesText());
                        break;

                    case "restart":
                        HandleAdminActionRequest(entityId, steamId, playerName, "restart");
                        break;

                    case "stop":
                        HandleAdminActionRequest(entityId, steamId, playerName, "stop");
                        break;

                    default:
                        {
                            var custom = chatbotConfig.CustomCommands.FirstOrDefault(c => c.Enabled && c.Name == cmd);
                            if (custom != null)
                            {
                                if (custom.CooldownMinutes > 0)
                                {
                                    var cooldownKey = steamId + "|" + custom.Name;
                                    lock (chatbotLock)
                                    {
                                        if (cooldowns.LastCustomCommandUtc.TryGetValue(cooldownKey, out var last))
                                        {
                                            var remaining = TimeSpan.FromMinutes(custom.CooldownMinutes) - (DateTime.UtcNow - last);
                                            if (remaining > TimeSpan.Zero)
                                            {
                                                ReplyToPlayer(entityId, tr.T("custom_cooldown", FormatTimeSpan(remaining)));
                                                return;
                                            }
                                        }
                                        cooldowns.LastCustomCommandUtc[cooldownKey] = DateTime.UtcNow;
                                        SaveCooldowns();
                                    }
                                }
                                var arg = parts.Length > 1 ? parts[1] : null;
                                DeliverCustomCommand(entityId, steamId, playerName, custom, arg);
                            }
                            else ReplyToPlayer(entityId, tr.T("unknown_command"));
                        }
                        break;
                }
            }
            catch (Exception ex) { modApi.LogError("ElbMod: HandleChatbotCommand: " + ex); }
        }

        void HandleWipeRequest(int entityId, string steamId, string playerName)
        {
            lock (chatbotLock)
            {
                if (cooldowns.LastWipeUtc.TryGetValue(steamId, out var last))
                {
                    var remaining = TimeSpan.FromHours(chatbotConfig.WipeCooldownHours) - (DateTime.UtcNow - last);
                    if (remaining > TimeSpan.Zero)
                    {
                        ReplyToPlayer(entityId, tr.T("wipe_cooldown", FormatTimeSpan(remaining)));
                        return;
                    }
                }
            }
            ShowDialog(entityId, tr.T("wipe_confirm_prompt"), new[] { tr.T("wipe_confirm_button"), tr.T("wipe_cancel_button") }, escIdx: 1, onButton: (buttonIdx, linkId) =>
            {
                if (buttonIdx == 0) Task.Run(() => HandleWipeConfirm(entityId, steamId, playerName));
            });
        }

        async Task HandleWipeConfirm(int entityId, string steamId, string playerName)
        {
            if (Debug) modApi.Log($"ElbMod: cb:wipe confirmed via button — kicking {playerName} ({steamId}) for wipe");

            byte factionGroup = 0;
            var factionId = 0;
            if (chatbotConfig.HardWipe)
            {
                try
                {
                    var pi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(entityId));
                    factionGroup = pi.factionGroup;
                    factionId = pi.factionId;
                }
                catch (Exception ex) { modApi.LogError("ElbMod: wipe: Request_Player_Info for hard wipe: " + ex); }
            }

            try { LegacyRequest(CmdId.Request_ConsoleCommand, new PString($"ban {steamId} 1h"), waitForResponse: false); }
            catch (Exception ex) { modApi.LogError("ElbMod: wipe ban: " + ex); }
            try { LegacyRequest(CmdId.Request_ConsoleCommand, new PString($"kick {playerName}"), waitForResponse: false); }
            catch (Exception ex) { modApi.LogError("ElbMod: wipe kick: " + ex); }

            try
            {
                var offline = false;
                for (int i = 0; i < 30; i++)
                {
                    await Task.Delay(1000);
                    var pd = modApi.Application.GetPlayerDataFor(entityId);
                    if (!pd.HasValue || !pd.Value.IsOnline) { offline = true; break; }
                }
                if (!offline)
                {
                    modApi.LogError($"ElbMod: wipe {steamId}: player did not go offline within 30s, file deletion cancelled");
                    return;
                }

                await Task.Delay(10000);

                var file = Path.Combine(modApi.Application.GetPathFor(AppFolder.SaveGame), "Players", steamId + ".ply");
                try { if (File.Exists(file)) File.Delete(file); }
                catch (Exception ex) { modApi.LogError("ElbMod: wipe: deleting file: " + ex); }

                if (File.Exists(file))
                {
                    modApi.LogError($"ElbMod: wipe {steamId}: failed to delete file — wipe failed");
                    return;
                }

                if (Debug) modApi.Log($"ElbMod: wipe {steamId}: file {file} removed");
                lock (chatbotLock)
                {
                    cooldowns.LastWipeUtc[steamId] = DateTime.UtcNow;
                    cooldowns.SurvivalClaimed.Remove(steamId);
                    SaveCooldowns();
                }

                QueueBackpackCleanup(steamId, entityId);

                if (chatbotConfig.HardWipe && factionGroup == 0 && factionId > 0)
                    QueueHardWipe(steamId, entityId, factionId);
            }
            finally
            {
                try { LegacyRequest(CmdId.Request_ConsoleCommand, new PString($"unban {steamId}"), waitForResponse: false); }
                catch (Exception ex) { modApi.LogError("ElbMod: wipe unban: " + ex); }
            }
        }

        void HandleAdminActionRequest(int entityId, string steamId, string playerName, string action)
        {
            if (!chatbotConfig.AdminSteamIds.Contains(steamId))
            {
                ReplyToPlayer(entityId, tr.T("unknown_command"));
                return;
            }
            var prompt = action == "stop" ? tr.T("stop_confirm_prompt") : tr.T("restart_confirm_prompt");
            var confirmButton = action == "stop" ? tr.T("stop_confirm_button") : tr.T("restart_confirm_button");
            ShowDialog(entityId, prompt, new[] { confirmButton, tr.T("wipe_cancel_button") }, escIdx: 1, onButton: (buttonIdx, linkId) =>
            {
                if (buttonIdx != 0) return;
                QueueServerAction(steamId, playerName, action);
                ReplyToPlayer(entityId, action == "stop" ? tr.T("stop_queued") : tr.T("restart_queued"));
            });
        }

        void QueueServerAction(string steamId, string playerName, string action)
        {
            try
            {
                var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "restart-queue");
                Directory.CreateDirectory(dir);
                var file = Path.Combine(dir, $"{steamId}_{DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()}.json");
                File.WriteAllText(file, JsonSerializer.Serialize(new { steamId, playerName, action, requestedAtUtc = DateTimeOffset.UtcNow.ToString("o") }));
                if (Debug) modApi.Log($"ElbMod: {action}: request from {playerName} ({steamId}) recorded to {file}");
            }
            catch (Exception ex) { modApi.LogError("ElbMod: QueueServerAction: " + ex); }
        }

        const int BagSlotCount = 40;

        void HandleSurvivalRequest(int entityId, string steamId)
        {
            if (chatbotConfig.SurvivalCredits <= 0 && chatbotConfig.SurvivalItems.Count == 0)
            {
                ReplyToPlayer(entityId, tr.T("survival_not_configured"));
                return;
            }
            lock (chatbotLock)
            {
                if (cooldowns.SurvivalClaimed.Contains(steamId))
                {
                    ReplyToPlayer(entityId, tr.T("survival_already_claimed"));
                    return;
                }
            }
            try
            {
                if (chatbotConfig.SurvivalCredits > 0)
                    LegacyRequest(CmdId.Request_Player_AddCredits, new IdCredits(entityId, chatbotConfig.SurvivalCredits));

                if (chatbotConfig.SurvivalItems.Count > 0)
                {
                    var inv = (Inventory)LegacyRequest(CmdId.Request_Player_GetInventory, new Id(entityId));
                    var existingBag = inv.bag ?? Array.Empty<ItemStack>();
                    var occupied = new HashSet<int>();
                    foreach (var s in existingBag)
                        occupied.Add(s.slotIdx);

                    var newBag = new List<ItemStack>(existingBag);
                    int nextSlot = 0;
                    foreach (var item in chatbotConfig.SurvivalItems)
                    {
                        while (occupied.Contains(nextSlot) && nextSlot < BagSlotCount) nextSlot++;
                        if (nextSlot >= BagSlotCount) break; 
                        occupied.Add(nextSlot);
                        newBag.Add(new ItemStack(item.ItemId, item.Count) { slotIdx = (byte)nextSlot });
                        nextSlot++;
                    }

                    LegacyRequest(CmdId.Request_Player_SetInventory, new Inventory
                    {
                        playerId = entityId,
                        toolbelt = inv.toolbelt,
                        bag = newBag.ToArray(),
                    });
                }

                lock (chatbotLock)
                {
                    cooldowns.SurvivalClaimed.Add(steamId);
                    SaveCooldowns();
                }
                ReplyToPlayer(entityId, tr.T("survival_granted"));
            }
            catch (Exception ex)
            {
                modApi.LogError("ElbMod: survival: " + ex);
                ReplyToPlayer(entityId, tr.T("getship_failed"));
            }
        }

        void QueueHardWipe(string steamId, int entityId, int factionId)
        {
            try
            {
                var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "hardwipe-queue");
                Directory.CreateDirectory(dir);
                var file = Path.Combine(dir, $"{steamId}_{DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()}.json");
                File.WriteAllText(file, JsonSerializer.Serialize(new { steamId, entityId, factionId }));
                if (Debug) modApi.Log($"ElbMod: hard wipe: request for faction {factionId} ({steamId}) recorded to {file}");
            }
            catch (Exception ex) { modApi.LogError("ElbMod: QueueHardWipe: " + ex); }
        }

        void QueueBackpackCleanup(string steamId, int entityId)
        {
            try
            {
                var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "wipe-cleanup-queue");
                Directory.CreateDirectory(dir);
                var file = Path.Combine(dir, $"{steamId}_{DateTimeOffset.UtcNow.ToUnixTimeMilliseconds()}.json");
                File.WriteAllText(file, JsonSerializer.Serialize(new { steamId, entityId }));
                if (Debug) modApi.Log($"ElbMod: wipe: backpack history cleanup request ({steamId}) recorded to {file}");
            }
            catch (Exception ex) { modApi.LogError("ElbMod: QueueBackpackCleanup: " + ex); }
        }

        class PendingHardWipeFile { public string playfield { get; set; } public List<int> ids { get; set; } }

        class StructureRefuelBody
        {
            public int id { get; set; }
            public string playfield { get; set; }
            public float? fuelPercent { get; set; }
            public float? oxygenPercent { get; set; }
            public float? pentaxidPercent { get; set; }
        }

        class StructureBackupPushBody
        {
            public int id { get; set; }
            public string playfield { get; set; }
            public string name { get; set; }
            public string typeName { get; set; }
            public float posX { get; set; }
            public float posY { get; set; }
            public float posZ { get; set; }
            public byte factionGroup { get; set; }
            public int factionId { get; set; }
            public object containers { get; set; }
            public object fuelTank { get; set; }
            public object oxygenTank { get; set; }
            public object pentaxidTank { get; set; }
            public object geometryBlocks { get; set; }
            public int geometryBlockCount { get; set; }
        }
        class ItemStackCommand
        {
            public int id { get; set; }
            public int count { get; set; }
            public int slotIdx { get; set; }
            public int ammo { get; set; }
            public int decay { get; set; }
        }
        class ContainerRestoreCommand
        {
            public int x { get; set; }
            public int y { get; set; }
            public int z { get; set; }
            public List<ItemStackCommand> items { get; set; }
        }
        class StructureCommand
        {
            public int id { get; set; }
            public float? fuelPercent { get; set; }
            public float? oxygenPercent { get; set; }
            public float? pentaxidPercent { get; set; }
            public List<ContainerRestoreCommand> restoreContainers { get; set; }
            public List<GeometryBlockCommand> restoreGeometry { get; set; }
            public bool? forceBackupNow { get; set; }
        }
        class PendingStructureCommandsFile { public string playfield { get; set; } public List<StructureCommand> commands { get; set; } }
        class GeometryBlockCommand
        {
            public int x { get; set; }
            public int y { get; set; }
            public int z { get; set; }
            public int type { get; set; }
            public int shape { get; set; }
            public int rot { get; set; }
            public bool active { get; set; }
        }
        class StructureRestoreContainersBody
        {
            public int id { get; set; }
            public string playfield { get; set; }
            public List<ContainerRestoreCommand> containers { get; set; }
        }
        class StructureGeometryRestoreBody
        {
            public int id { get; set; }
            public string playfield { get; set; }
            public List<GeometryBlockCommand> blocks { get; set; }
        }
        class StructureBackupForceBody
        {
            public int id { get; set; }
            public string playfield { get; set; }
        }

        static string SanitizeForFilename(string s)
        {
            if (string.IsNullOrEmpty(s)) return "_";
            var sb = new StringBuilder(s.Length);
            foreach (var c in s)
                sb.Append((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '.' || c == '-' ? c : '_');
            return sb.ToString();
        }

        void ProcessPendingHardWipeDestroys(string playfield)
        {
            try
            {
                var dir = Path.Combine(Path.GetDirectoryName(ChatbotSettingsPath), "hardwipe-pending");
                var file = Path.Combine(dir, SanitizeForFilename(playfield) + ".json");
                if (!File.Exists(file)) return;

                var pending = JsonSerializer.Deserialize<PendingHardWipeFile>(File.ReadAllText(file));
                if (pending?.ids == null || pending.ids.Count == 0) { File.Delete(file); return; }

                Thread.Sleep(5000);

                var existingIds = FetchExistingStructureIds(playfield);

                var failed = new List<int>();
                using (var client = new WebClient())
                {
                    client.Headers[HttpRequestHeader.ContentType] = "application/json";
                    foreach (var id in pending.ids)
                    {
                        if (existingIds != null && !existingIds.Contains(id))
                        {
                            if (Debug) modApi.Log($"ElbMod (PfServer): hardwipe pending {id} on {playfield} already gone (removed itself/by the engine) — removing from queue");
                            continue;
                        }
                        try
                        {
                            client.UploadString("http://127.0.0.1:31914/structures/destroy",
                                JsonSerializer.Serialize(new { id, playfield }));
                        }
                        catch (Exception ex)
                        {
                            modApi.LogError($"ElbMod (PfServer): hardwipe pending destroy {id} on {playfield}: " + ex);
                            failed.Add(id);
                        }
                    }
                }

                if (failed.Count == 0)
                {
                    File.Delete(file);
                    if (Debug) modApi.Log($"ElbMod (PfServer): processed {pending.ids.Count} pending hard-wipe deletions on {playfield}");
                }
                else
                {
                    File.WriteAllText(file, JsonSerializer.Serialize(new { playfield, ids = failed }));
                    modApi.LogError($"ElbMod (PfServer): {failed.Count} of {pending.ids.Count} pending deletions on {playfield} failed — remaining in queue");
                }
            }
            catch (Exception ex) { modApi.LogError("ElbMod: ProcessPendingHardWipeDestroys: " + ex); }
        }

        HashSet<int> FetchExistingStructureIds(string playfield)
        {
            try
            {
                using (var client = new WebClient())
                {
                    var json = client.DownloadString(
                        "http://127.0.0.1:31914/structures?playfield=" + Uri.EscapeDataString(playfield));
                    using (var doc = JsonDocument.Parse(json))
                    {
                        var ids = new HashSet<int>();
                        foreach (var item in doc.RootElement.EnumerateArray())
                            if (item.TryGetProperty("id", out var idProp)) ids.Add(idProp.GetInt32());
                        return ids;
                    }
                }
            }
            catch (Exception ex)
            {
                modApi.LogError($"ElbMod (PfServer): FetchExistingStructureIds({playfield}): " + ex);
                return null;
            }
        }

        static bool IsVehicleType(byte type) => type == 3 || type == 4 || type == 5;

        static byte StructureTypeByte(string type)
        {
            switch ((type ?? "").Trim().ToUpperInvariant())
            {
                case "BA": return 2;
                case "CV": return 3;
                case "SV": return 4;
                case "HV": return 5;
                case "ASTVOXEL": return 7;
                default: return 0;
            }
        }

        static string StructureTypeLabel(byte type)
        {
            switch (type)
            {
                case 2: return "BA";
                case 3: return "CV";
                case 4: return "SV";
                case 5: return "HV";
                case 7: return "AstVoxel";
                default: return "?";
            }
        }

        List<GlobalStructureInfo> FindMyStructures(int entityId, string playfield)
        {
            var playerFactionId = 0;
            try
            {
                var pi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(entityId));
                if (pi.factionGroup == 0 && pi.factionId > 0) playerFactionId = pi.factionId;
            }
            catch (Exception ex) { modApi.LogError("ElbMod: FindMyStructures: Request_Player_Info: " + ex); }

            var done = new ManualResetEventSlim(false);
            var result = new List<GlobalStructureInfo>();
            bool accepted = modApi.Application.GetStructures(playfield, null, null, structures =>
            {
                foreach (var s in structures)
                    if ((s.factionGroup == 1 && s.factionId == entityId) ||
                        (s.factionGroup == 0 && playerFactionId > 0 && s.factionId == playerFactionId))
                        result.Add(s);
                done.Set();
            });
            if (!accepted || !done.Wait(TimeSpan.FromSeconds(10))) return new List<GlobalStructureInfo>();
            return result;
        }

        void HandleGetList(int entityId, string playfield)
        {
            if (string.IsNullOrEmpty(playfield))
            {
                ReplyToPlayer(entityId, tr.T("no_playfield"));
                return;
            }
            var mine = FindMyStructures(entityId, playfield);
            if (mine.Count == 0)
            {
                ReplyToPlayer(entityId, tr.T("myships_none"));
                return;
            }
            ReplyToPlayer(entityId, tr.T("myships_list", FormatStructureList(mine)));
        }

        string FormatStructureList(List<GlobalStructureInfo> structures) =>
            string.Join("\n", structures.ConvertAll(s => tr.T("myships_item", s.name, StructureTypeLabel(s.type), s.id)));

        void HandleShipRequest(int entityId, string steamId, string playfield, int? requestedId)
        {
            lock (chatbotLock)
            {
                if (cooldowns.LastTeleportUtc.TryGetValue(steamId, out var last))
                {
                    var remaining = TimeSpan.FromMinutes(chatbotConfig.VehicleTeleportCooldownMinutes) - (DateTime.UtcNow - last);
                    if (remaining > TimeSpan.Zero)
                    {
                        ReplyToPlayer(entityId, tr.T("getship_cooldown", FormatTimeSpan(remaining)));
                        return;
                    }
                }
            }

            if (string.IsNullOrEmpty(playfield))
            {
                ReplyToPlayer(entityId, tr.T("no_playfield"));
                return;
            }

            var mine = FindMyStructures(entityId, playfield);

            if (requestedId.HasValue)
            {
                GlobalStructureInfo? found = null;
                foreach (var s in mine)
                    if (s.id == requestedId.Value) { found = s; break; }
                if (found == null)
                {
                    ReplyToPlayer(entityId, tr.T("getship_not_found", requestedId.Value));
                    return;
                }
                if (!IsVehicleType(found.Value.type))
                {
                    ReplyToPlayer(entityId, tr.T("get_not_a_vehicle"));
                    return;
                }
                TeleportShipToPlayer(entityId, steamId, found.Value);
                return;
            }

            var vehicles = mine.Where(s => IsVehicleType(s.type)).ToList();
            if (vehicles.Count == 0)
            {
                ReplyToPlayer(entityId, tr.T("ship_none"));
                return;
            }
            if (vehicles.Count == 1)
            {
                TeleportShipToPlayer(entityId, steamId, vehicles[0]);
                return;
            }
            ReplyToPlayer(entityId, tr.T("getship_need_id"));
        }

        void HandleDestroyRequest(int entityId, string playfield, int requestedId)
        {
            if (string.IsNullOrEmpty(playfield))
            {
                ReplyToPlayer(entityId, tr.T("no_playfield"));
                return;
            }
            var mine = FindMyStructures(entityId, playfield);
            GlobalStructureInfo? found = null;
            foreach (var s in mine)
                if (s.id == requestedId) { found = s; break; }
            if (found == null)
            {
                ReplyToPlayer(entityId, tr.T("getship_not_found", requestedId));
                return;
            }
            try
            {
                var pi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(entityId));
                var dist = Distance(pi.pos, found.Value.pos);
                if (dist > VehicleActionRadiusMeters)
                {
                    ReplyToPlayer(entityId, tr.T("action_too_far", (int)dist, (int)VehicleActionRadiusMeters));
                    return;
                }
                LegacyRequest(CmdId.Request_Entity_Destroy2, new IdPlayfield(found.Value.id, playfield));
                ReplyToPlayer(entityId, tr.T("destroy_done", found.Value.name));
            }
            catch (Exception ex)
            {
                modApi.LogError("ElbMod: destroy: " + ex);
                ReplyToPlayer(entityId, tr.T("getship_failed"));
            }
        }

        void TeleportShipToPlayer(int entityId, string steamId, GlobalStructureInfo ship)
        {
            try
            {
                var pi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(entityId));
                var dist = Distance(pi.pos, ship.pos);
                if (dist > VehicleActionRadiusMeters)
                {
                    ReplyToPlayer(entityId, tr.T("action_too_far", (int)dist, (int)VehicleActionRadiusMeters));
                    return;
                }
                var pos = new PVector3(pi.pos.x, pi.pos.y + 5, pi.pos.z);
                LegacyRequest(CmdId.Request_Entity_Teleport,
                    new IdPositionRotation { id = ship.id, pos = pos, rot = new PVector3(0, 0, 0) });
                lock (chatbotLock)
                {
                    cooldowns.LastTeleportUtc[steamId] = DateTime.UtcNow;
                    SaveCooldowns();
                }
            }
            catch (Exception ex)
            {
                modApi.LogError("ElbMod: getship: " + ex);
                ReplyToPlayer(entityId, tr.T("getship_failed"));
            }
        }

        void ReplyToPlayer(int entityId, string text)
        {
            ShowDialog(entityId, text, new[] { tr.T("ok") }, escIdx: 0, onButton: null);
        }

        class CommandListing
        {
            public readonly string Id, Invoke;
            public CommandListing(string id, string invoke) { Id = id; Invoke = invoke; }
        }
        static readonly CommandListing[] BasicCommandOrder =
        {
            new CommandListing("?", "?"), new CommandListing("wipe", "wipe"),
            new CommandListing("get", "get"), new CommandListing("ship", "ship:id"),
            new CommandListing("destroy", "destroy:id"), new CommandListing("time", "time"),
            new CommandListing("survival", "survival"), new CommandListing("rules", "rules"),
        };
        static readonly CommandListing[] AdminCommandOrder =
        {
            new CommandListing("restart", "restart"), new CommandListing("stop", "stop"),
        };

        string DefaultCommandDescription(string id)
        {
            switch (id)
            {
                case "?": return tr.T("cmd_help");
                case "wipe": return tr.T("cmd_wipe", chatbotConfig.WipeCooldownHours);
                case "get": return tr.T("cmd_get");
                case "ship": return tr.T("cmd_ship", chatbotConfig.VehicleTeleportCooldownMinutes);
                case "destroy": return tr.T("cmd_destroy");
                case "time": return tr.T("cmd_time");
                case "survival": return tr.T("cmd_survival");
                case "rules": return tr.T("cmd_rules");
                case "restart": return tr.T("cmd_restart");
                case "stop": return tr.T("cmd_stop");
                default: return "";
            }
        }

        string RulesText() =>
            string.IsNullOrWhiteSpace(chatbotConfig.RulesText) ? tr.T("rules_default_text") : chatbotConfig.RulesText;

        string CommandDescription(string id) =>
            chatbotConfig.CommandDescriptions.TryGetValue(id, out var d) && !string.IsNullOrWhiteSpace(d)
                ? d
                : DefaultCommandDescription(id);

        static string FormatCommandLine(string cbName, string desc)
        {
            var name = "<color=yellow>" + cbName + "</color>";
            return string.IsNullOrWhiteSpace(desc) ? name : name + "\n    <color=white>" + desc + "</color>";
        }

        string BuildHelpText(string steamId)
        {
            var blocks = BasicCommandOrder.Where(c => IsCommandEnabled(c.Id))
                .Select(c => FormatCommandLine("cb:" + c.Invoke, CommandDescription(c.Id)))
                .ToList();

            var customCmds = chatbotConfig.CustomCommands.Where(c => c.Enabled).ToList();
            if (customCmds.Count > 0)
            {
                blocks.Add(tr.T("help_custom"));
                blocks.AddRange(customCmds.Select(c => FormatCommandLine("cb:" + c.Name, c.GameDescription)));
            }

            if (chatbotConfig.AdminSteamIds.Contains(steamId))
            {
                var admin = AdminCommandOrder.Where(c => IsCommandEnabled(c.Id)).ToList();
                if (admin.Count > 0)
                {
                    var adminLines = admin.Select(c => FormatCommandLine("cb:" + c.Invoke, CommandDescription(c.Id))).ToList();
                    adminLines[0] = "<u>" + tr.T("help_admin_header") + "</u>\n" + adminLines[0];
                    blocks.AddRange(adminLines);
                }
            }

            return "<size=80%>" + string.Join("\n\n", blocks) + "</size>";
        }

        void ShowDialog(int entityId, string bodyText, string[] buttonTexts, int escIdx, Action<int, string> onButton)
        {
            try
            {
                modApi.Application.ShowDialogBox(entityId, new DialogConfig
                {
                    TitleText = "",
                    BodyText = bodyText,
                    ButtonTexts = buttonTexts,
                    ButtonIdxForEsc = escIdx,
                    ButtonIdxForEnter = buttonTexts.Length == 1 ? 0 : -1,
                }, (buttonIdx, linkId, inputContent, playerId, customValue) => onButton?.Invoke(buttonIdx, linkId), 0);
            }
            catch (Exception ex) { modApi.LogError("ElbMod: ShowDialog: " + ex); }
        }

        string FormatTimeSpan(TimeSpan t)
        {
            if (t.TotalHours >= 1) return $"{(int)t.TotalHours} {tr.T("time_hours_short")} {t.Minutes} {tr.T("time_minutes_short")}";
            if (t.TotalMinutes >= 1) return $"{(int)t.TotalMinutes} {tr.T("time_minutes_short")}";
            return $"{Math.Max(0, (int)t.TotalSeconds)} {tr.T("time_seconds_short")}";
        }

        string ChatbotCooldownsPath =>
            Path.Combine(modApi.Application.GetPathFor(AppFolder.SaveGame), "Mods", "ElbMod", "ChatbotCooldowns.json");

        void LoadCooldowns()
        {
            try
            {
                var file = ChatbotCooldownsPath;
                if (File.Exists(file))
                    cooldowns = JsonSerializer.Deserialize<ChatbotCooldowns>(File.ReadAllText(file)) ?? new ChatbotCooldowns();
            }
            catch (Exception ex) { modApi.LogError("ElbMod: LoadCooldowns: " + ex); }
        }

        void SaveCooldowns()
        {
            try
            {
                var dir = Path.Combine(modApi.Application.GetPathFor(AppFolder.SaveGame), "Mods", "ElbMod");
                Directory.CreateDirectory(dir);
                File.WriteAllText(ChatbotCooldownsPath, JsonSerializer.Serialize(cooldowns));
            }
            catch (Exception ex) { modApi.LogError("ElbMod: SaveCooldowns: " + ex); }
        }


        public void Game_Start(ModGameAPI dediApi)
        {
            legacyApi = dediApi;
            if (Debug) dediApi?.Console_Write("ElbMod: Game_Start (legacy ModInterface) called");
        }

        public void Game_Exit() { }

        public void Game_Update() { }

        public void Game_Event(CmdId eventId, ushort seqNr, object data)
        {
            if (Debug) legacyApi?.Console_Write($"ElbMod: Game_Event {eventId} seq={seqNr} pending={pending.ContainsKey(seqNr)}");
            if (!pending.TryRemove(seqNr, out var tcs)) return;
            if (eventId == CmdId.Event_Error) tcs.TrySetException(new Exception($"game error: {ErrorTypeName((data as ErrorInfo)?.errorType)}"));
            else tcs.TrySetResult(data);
        }

        static string ErrorTypeName(ErrorType? t)
        {
            if (t == null) return "?";
            switch ((byte)t.Value)
            {
                case 0: return "None";
                case 1: return "MissingParameter";
                case 2: return "PlayerIdNotFound";
                case 3: return "PlayfieldOfPlayerNotFound";
                case 4: return "PlayfieldConnectionNotFound";
                case 5: return "EntityIdNotFound";
                case 6: return "CouldNotAddItemToInventory";
                case 7: return "CouldNotRemoveItemFromInventory";
                case 8: return "BlueprintError";
                case 9: return "NotEnoughCredits";
                case 10: return "EntityTypeNotSupported";
                case 11: return "NoIdlePlayfieldFound";
                case 12: return "PlayfieldCannotBeLoaded";
                case 13: return "PlayfieldAlreadyLoaded";
                case 14: return "CommandNotImplemented";
                case 15: return "IOError";
                case 16: return "EntityNotLocalToPlayfield";
                default: return $"code {(byte)t.Value}";
            }
        }

        async Task<object> LegacyRequestAsync(CmdId cmd, object data, bool waitForResponse = true)
        {
            var seq = (ushort)Interlocked.Increment(ref nextSeq);
            var tcs = new TaskCompletionSource<object>();
            pending[seq] = tcs;
            if (!legacyApi.Game_Request(cmd, seq, data))
            {
                pending.TryRemove(seq, out _);
                throw new Exception($"{cmd}: rejected by the engine");
            }
            if (!waitForResponse)
            {
                _ = Task.Delay(LegacyTimeout).ContinueWith(t => pending.TryRemove(seq, out _));
                return null;
            }

            var winner = await Task.WhenAny(tcs.Task, Task.Delay(LegacyTimeout));
            pending.TryRemove(seq, out _);
            if (winner != tcs.Task) throw new TimeoutException($"{cmd} did not respond within {LegacyTimeout.TotalSeconds}s");
            return await tcs.Task;
        }

        object LegacyRequest(CmdId cmd, object data, bool waitForResponse = true) =>
            LegacyRequestAsync(cmd, data, waitForResponse).GetAwaiter().GetResult();


        void ListenLoop()
        {
            while (listener.IsListening)
            {
                HttpListenerContext ctx;
                try { ctx = listener.GetContext(); }
                catch (Exception) { break; }

                try { HandleRequest(ctx); }
                catch (Exception ex)
                {
                    modApi.LogError("ElbMod: " + ex);
                    try
                    {
                        ctx.Response.StatusCode = 500;
                        WriteJson(ctx.Response, new { error = ex.Message });
                    }
                    catch {  }
                }
            }
        }

        void HandleRequest(HttpListenerContext ctx)
        {
            var req = ctx.Request;
            var resp = ctx.Response;
            var path = req.Url.AbsolutePath;

            if (path == "/chat" && req.HttpMethod == "GET")
            {
                List<ChatEntry> snapshot;
                lock (chatLock) snapshot = new List<ChatEntry>(recentChat);
                WriteJson(resp, snapshot);
                return;
            }

            if (path == "/chat" && req.HttpMethod == "POST")
            {
                var body = ReadJson<SendChatRequest>(req);
                var text = body?.text ?? "";

                const string ServerIdentity = "__server__";
                global::Eleon.Modding.PlayerData? asPlayer = null;
                var asServer = body?.asSteamId == ServerIdentity;
                if (!asServer && !string.IsNullOrEmpty(body?.asSteamId))
                {
                    asPlayer = FindPlayerBySteamId(body.asSteamId);
                    if (!asPlayer.HasValue || !asPlayer.Value.IsOnline)
                    {
                        resp.StatusCode = 400;
                        WriteJson(resp, new { error = "asSteamId: игрок не найден или оффлайн" });
                        return;
                    }
                }

                var msg = new MessageData { Text = text };
                string senderLabel;
                if (asPlayer.HasValue)
                {
                    msg.SenderType = global::Eleon.SenderType.Player;
                    msg.SenderEntityId = asPlayer.Value.EntityId;
                    senderLabel = asPlayer.Value.PlayerName;
                }
                else if (asServer)
                {
                    var name = string.IsNullOrEmpty(body?.senderName) ? "Сервер" : body.senderName;
                    msg.SenderType = global::Eleon.SenderType.System;
                    msg.SenderNameOverride = name;
                    senderLabel = name;
                }
                else
                {
                    msg.SenderType = global::Eleon.SenderType.ServerInfo;
                    msg.SenderNameOverride = "admin";
                    senderLabel = "admin";
                }

                string target = null;

                if (!string.IsNullOrEmpty(body?.recipientSteamId))
                {
                    var recipient = FindPlayerBySteamId(body.recipientSteamId);
                    if (!recipient.HasValue || !recipient.Value.IsOnline)
                    {
                        resp.StatusCode = 400;
                        WriteJson(resp, new { error = "recipientSteamId: игрок не найден или оффлайн" });
                        return;
                    }
                    msg.Channel = global::Eleon.MsgChannel.SinglePlayer;
                    msg.RecipientEntityId = recipient.Value.EntityId;
                    target = recipient.Value.PlayerName;
                }
                else if ((body?.channel ?? "").Trim().ToLowerInvariant() == "alliance")
                {
                    var raw = body?.allianceFactionIds ?? "";
                    var parts = raw.Split(',');
                    if (parts.Length != 2)
                    {
                        resp.StatusCode = 400;
                        WriteJson(resp, new { error = "channel=alliance требует allianceFactionIds=\"id1:tag1,id2:tag2\"" });
                        return;
                    }
                    var tags = new string[2];
                    for (int i = 0; i < 2; i++)
                    {
                        var sep = parts[i].IndexOf(':');
                        var idPart = sep >= 0 ? parts[i].Substring(0, sep) : parts[i];
                        if (!int.TryParse(idPart, out var factionId))
                        {
                            resp.StatusCode = 400;
                            WriteJson(resp, new { error = "allianceFactionIds: некорректный id в паре" });
                            return;
                        }
                        tags[i] = sep >= 0 ? parts[i].Substring(sep + 1) : idPart;
                        var faction = new global::FactionData { Group = global::FactionGroup.Faction, Id = factionId };
                        var pairMsg = new MessageData
                        {
                            Text = text,
                            SenderType = msg.SenderType,
                            SenderEntityId = msg.SenderEntityId,
                            SenderNameOverride = msg.SenderNameOverride,
                            Channel = global::Eleon.MsgChannel.Faction,
                            RecipientFaction = faction,
                            SenderFaction = faction,
                        };
                        modApi.Application.SendChatMessage(pairMsg);
                    }
                    target = tags[0] + " + " + tags[1];
                    lock (chatLock)
                    {
                        recentChat.Add(new ChatEntry
                        {
                            sender = senderLabel,
                            steamId = asPlayer?.SteamId,
                            text = text,
                            gameTime = 0,
                            timestamp = DateTimeOffset.UtcNow.ToString("o"),
                            channel = "alliance",
                            target = target,
                        });
                        if (recentChat.Count > MaxRecentChat)
                            recentChat.RemoveAt(0);
                    }
                    WriteJson(resp, new { ok = true });
                    return;
                }
                else
                {
                    msg.Channel = ParseChannel(body?.channel);
                    if (msg.Channel == global::Eleon.MsgChannel.Faction)
                    {
                        var raw = body?.factionId ?? "";
                        var sep = raw.IndexOf(':');
                        var idPart = sep >= 0 ? raw.Substring(0, sep) : raw;
                        if (!int.TryParse(idPart, out var factionId))
                        {
                            resp.StatusCode = 400;
                            WriteJson(resp, new { error = "channel=faction требует factionId" });
                            return;
                        }
                        target = sep >= 0 ? raw.Substring(sep + 1) : null;
                        var faction = new global::FactionData { Group = global::FactionGroup.Faction, Id = factionId };
                        msg.RecipientFaction = faction;
                        msg.SenderFaction = faction;
                    }
                }

                modApi.Application.SendChatMessage(msg);

                lock (chatLock)
                {
                    recentChat.Add(new ChatEntry
                    {
                        sender = senderLabel,
                        steamId = asPlayer?.SteamId,
                        text = text,
                        gameTime = 0,
                        timestamp = DateTimeOffset.UtcNow.ToString("o"),
                        channel = msg.Channel.ToString().ToLowerInvariant(),
                        target = target,
                    });
                    if (recentChat.Count > MaxRecentChat)
                        recentChat.RemoveAt(0);
                }
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/alliances/pairs" && req.HttpMethod == "GET")
            {
                var idsParam = req.QueryString["ids"] ?? "";
                var ids = idsParam.Split(',')
                    .Select(s => int.TryParse(s.Trim(), out var n) ? n : (int?)null)
                    .Where(n => n.HasValue).Select(n => n.Value).Distinct().ToList();
                var pairs = new List<object>();
                for (int i = 0; i < ids.Count; i++)
                {
                    for (int j = i + 1; j < ids.Count; j++)
                    {
                        try
                        {
                            var answer = (AlliancesFaction)LegacyRequest(
                                CmdId.Request_AlliancesFaction,
                                new AlliancesFaction(ids[i], ids[j], false));
                            if (answer != null && answer.isAllied)
                                pairs.Add(new { faction1Id = ids[i], faction2Id = ids[j] });
                        }
                        catch (Exception ex)
                        {
                            modApi.LogError($"ElbMod: /alliances/pairs {ids[i]}+{ids[j]}: {ex}");
                        }
                    }
                }
                WriteJson(resp, pairs);
                return;
            }

            if (path == "/players" && req.HttpMethod == "GET")
            {
                var players = new List<object>();
                foreach (var id in modApi.Application.GetPlayerEntityIds())
                {
                    var pd = modApi.Application.GetPlayerDataFor(id);
                    if (!pd.HasValue) continue;

                    object info = null;
                    if (pd.Value.IsOnline)
                    {
                        try
                        {
                            var pi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(id));
                            info = new
                            {
                                clientId = pi.clientId,
                                pos = new { x = pi.pos.x, y = pi.pos.y, z = pi.pos.z },
                                rot = new { x = pi.rot.x, y = pi.rot.y, z = pi.rot.z },
                                startPlayfield = pi.startPlayfield,
                                steamOwnerId = pi.steamOwnerId,
                                origin = pi.origin,
                                factionGroup = pi.factionGroup,
                                factionId = pi.factionId,
                                factionRole = pi.factionRole,
                                health = pi.health,
                                healthMax = pi.healthMax,
                                oxygen = pi.oxygen,
                                oxygenMax = pi.oxygenMax,
                                stamina = pi.stamina,
                                staminaMax = pi.staminaMax,
                                food = pi.food,
                                foodMax = pi.foodMax,
                                radiation = pi.radiation,
                                radiationMax = pi.radiationMax,
                                bodyTemp = pi.bodyTemp,
                                bodyTempMax = pi.bodyTempMax,
                                kills = pi.kills,
                                died = pi.died,
                                credits = pi.credits,
                                exp = pi.exp,
                                upgrade = pi.upgrade,
                                ping = pi.ping,
                                permission = pi.permission,
                                bpRemainingTime = pi.bpRemainingTime,
                                bpInFactory = pi.bpInFactory,
                                bpResourcesInFactory = pi.bpResourcesInFactory,
                                producedPrefabs = pi.producedPrefabs,
                            };
                        }
                        catch (Exception ex)
                        {
                            modApi.LogError("ElbMod: /players: Request_Player_Info for " + id + ": " + ex);
                        }
                    }

                    players.Add(new
                    {
                        entityId = pd.Value.EntityId,
                        name = pd.Value.PlayerName,
                        steamId = pd.Value.SteamId,
                        playfield = pd.Value.PlayfieldName,
                        online = pd.Value.IsOnline,
                        info,
                    });
                }
                WriteJson(resp, players);
                return;
            }

            if (path == "/playfields" && req.HttpMethod == "GET")
            {
                var pfs = new List<object>();
                foreach (var pf in modApi.Application.GetAllPlayfields())
                    pfs.Add(new { name = pf.PlayfieldName, type = pf.PlayfieldType.ToString(), isInstance = pf.IsInstance });
                WriteJson(resp, pfs);
                return;
            }

            if (path == "/playfield-servers" && req.HttpMethod == "GET")
            {
                WriteJson(resp, modApi.Application.GetPfServerInfos());
                return;
            }

            if (path == "/items" && req.HttpMethod == "GET")
            {
                var file = Path.Combine(modApi.Application.GetPathFor(AppFolder.SaveGame), "Mods", "ElbMod", ItemMappingFileName);
                if (!File.Exists(file))
                {
                    resp.StatusCode = 503;
                    WriteJson(resp, new { error = "item mapping ещё не записан PfServer-инстансом (см. " + file + ")" });
                    return;
                }
                resp.ContentType = "application/json";
                var bytes = Encoding.UTF8.GetBytes(File.ReadAllText(file));
                resp.ContentLength64 = bytes.Length;
                resp.OutputStream.Write(bytes, 0, bytes.Length);
                resp.OutputStream.Close();
                return;
            }

            if (path == "/structures" && req.HttpMethod == "GET")
            {
                HandleGetStructures(req, resp);
                return;
            }

            if (path == "/structures/all" && req.HttpMethod == "GET")
            {
                HandleGetAllStructures(resp);
                return;
            }

            if (path == "/structures/backup/push" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureBackupPushBody>(req);
                var tmpDir = Path.Combine(modApi.Application.GetPathFor(AppFolder.SaveGame), "Mods", "ElbMod", "StructureBackupTmp");
                var tmpPath = Path.Combine(tmpDir, body.id + "_" + DateTimeOffset.UtcNow.ToUnixTimeSeconds() + ".dat");
                try
                {
                    Directory.CreateDirectory(tmpDir);
                    byte[] bytes;
                    try
                    {
                        LegacyRequest(CmdId.Request_Entity_Export, new EntityExportInfo
                        {
                            id = body.id,
                            playfield = body.playfield,
                            filePath = tmpPath,
                            isForceUnload = false,
                        });
                        bytes = File.ReadAllBytes(tmpPath);
                    }
                    finally { try { File.Delete(tmpPath); } catch { } }

                    var payload = new
                    {
                        id = body.id,
                        playfield = body.playfield,
                        name = body.name,
                        typeName = body.typeName,
                        posX = body.posX,
                        posY = body.posY,
                        posZ = body.posZ,
                        factionGroup = body.factionGroup,
                        factionId = body.factionId,
                        containers = body.containers,
                        fuelTank = body.fuelTank,
                        oxygenTank = body.oxygenTank,
                        pentaxidTank = body.pentaxidTank,
                        blueprintBase64 = Convert.ToBase64String(bytes),
                        geometryBlocks = body.geometryBlocks,
                        geometryBlockCount = body.geometryBlockCount,
                    };
                    lock (structureBackupQueueLock) { structureBackupQueue.Add(JsonSerializer.SerializeToElement(payload)); }
                    if (Debug) modApi.Log($"ElbMod: structure backup {body.id} exported and added to the queue");
                }
                catch (Exception ex) { modApi.LogError($"ElbMod: /structures/backup/push (structure {body.id}): " + ex); }
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/backup/pending" && req.HttpMethod == "GET")
            {
                List<JsonElement> batch;
                lock (structureBackupQueueLock)
                {
                    batch = new List<JsonElement>(structureBackupQueue);
                    structureBackupQueue.Clear();
                }
                WriteJson(resp, batch);
                return;
            }

            if (path == "/structures/refuel" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureRefuelBody>(req);
                if (string.IsNullOrEmpty(body.playfield))
                {
                    resp.StatusCode = 400;
                    WriteJson(resp, new { error = "playfield required" });
                    return;
                }
                EnqueueStructureCommand(body.playfield, body.id, cmd =>
                {
                    cmd.fuelPercent = body.fuelPercent;
                    cmd.oxygenPercent = body.oxygenPercent;
                    cmd.pentaxidPercent = body.pentaxidPercent;
                });
                WriteJson(resp, new { ok = true, queued = true });
                return;
            }

            if (path == "/structures/backup/restore-containers" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureRestoreContainersBody>(req);
                if (string.IsNullOrEmpty(body.playfield))
                {
                    resp.StatusCode = 400;
                    WriteJson(resp, new { error = "playfield required" });
                    return;
                }
                EnqueueStructureCommand(body.playfield, body.id, cmd =>
                {
                    cmd.restoreContainers = body.containers ?? new List<ContainerRestoreCommand>();
                });
                WriteJson(resp, new { ok = true, queued = true });
                return;
            }

            if (path == "/structures/geometry/restore" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureGeometryRestoreBody>(req);
                if (string.IsNullOrEmpty(body.playfield))
                {
                    resp.StatusCode = 400;
                    WriteJson(resp, new { error = "playfield required" });
                    return;
                }
                EnqueueStructureCommand(body.playfield, body.id, cmd =>
                {
                    cmd.restoreGeometry = body.blocks ?? new List<GeometryBlockCommand>();
                });
                WriteJson(resp, new { ok = true, queued = true });
                return;
            }

            if (path == "/structures/backup/force" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureBackupForceBody>(req);
                if (string.IsNullOrEmpty(body.playfield))
                {
                    resp.StatusCode = 400;
                    WriteJson(resp, new { error = "playfield required" });
                    return;
                }
                EnqueueStructureCommand(body.playfield, body.id, cmd => { cmd.forceBackupNow = true; });
                WriteJson(resp, new { ok = true, queued = true });
                return;
            }

            if (path == "/players/inventory" && req.HttpMethod == "GET")
            {
                var playerId = int.Parse(req.QueryString["playerId"] ?? "0");
                var inv = (Inventory)LegacyRequest(CmdId.Request_Player_GetInventory, new Id(playerId));
                WriteJson(resp, new
                {
                    playerId = inv.playerId,
                    toolbelt = ToDto(inv.toolbelt),
                    bag = ToDto(inv.bag),
                });
                return;
            }

            if (path == "/players/inventory" && req.HttpMethod == "POST")
            {
                HandleSetInventory(req, resp);
                return;
            }

            if (path == "/players/additem" && req.HttpMethod == "POST")
            {
                var body = ReadJson<AddItemBody>(req);
                LegacyRequest(CmdId.Request_Player_AddItem,
                    new IdItemStack(body.playerId, new ItemStack(body.itemId, body.count)));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/players/removeitem" && req.HttpMethod == "POST")
            {
                var body = ReadJson<AddItemBody>(req);
                LegacyRequest(CmdId.Request_Player_AddItem,
                    new IdItemStack(body.playerId, new ItemStack(body.itemId, -Math.Abs(body.count))));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/players/teleport" && req.HttpMethod == "POST")
            {
                HandleTeleport(req, resp);
                return;
            }

            if (path == "/players/setinfo" && req.HttpMethod == "POST")
            {
                HandleSetPlayerInfo(req, resp);
                return;
            }

            if (path == "/players/addcredits" && req.HttpMethod == "POST")
            {
                var body = ReadJson<AddCreditsBody>(req);
                LegacyRequest(CmdId.Request_Player_AddCredits, new IdCredits(body.playerId, body.amount));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/players/factory/finish" && req.HttpMethod == "POST")
            {
                var body = ReadJson<IdBody>(req);
                LegacyRequest(CmdId.Request_Blueprint_Finish, new Id(body.id));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/destroy" && req.HttpMethod == "POST")
            {
                var body = ReadJson<IdBody>(req);
                if (!string.IsNullOrEmpty(body.playfield))
                    LegacyRequest(CmdId.Request_Entity_Destroy2, new IdPlayfield(body.id, body.playfield));
                else
                    LegacyRequest(CmdId.Request_Entity_Destroy, new Id(body.id));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/touch" && req.HttpMethod == "POST")
            {
                var body = ReadJson<IdBody>(req);
                LegacyRequest(CmdId.Request_Structure_Touch, new Id(body.id));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/faction" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureFactionBody>(req);
                LegacyRequest(CmdId.Request_ConsoleCommand, new PString($"faction entity '{body.abbrev}' {body.id}"));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/move" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureMoveBody>(req);
                var cmd = string.Format(CultureInfo.InvariantCulture,
                    "setposition {0} {1},{2},{3}", body.id, body.x, body.y, body.z);
                LegacyRequest(CmdId.Request_ConsoleCommand, new PString(cmd));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/repair" && req.HttpMethod == "POST")
            {
                var body = ReadJson<StructureRepairBody>(req);
                var stats = (PlayfieldStats)LegacyRequest(CmdId.Request_Playfield_Stats, new PString(body.playfield));
                LegacyRequest(CmdId.Request_ConsoleCommand,
                    new PString($"remoteex pf={stats.processId} entity {body.id} -repair"));
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/structures/spawn" && req.HttpMethod == "POST")
            {
                HandleSpawnStructure(req, resp);
                return;
            }

            if (path == "/message/all" && req.HttpMethod == "POST")
            {
                var body = ReadJson<MessageAllBody>(req);
                LegacyRequest(CmdId.Request_InGameMessage_AllPlayers,
                    new IdMsgPrio(0, body.text ?? "", (byte)body.prio, body.duration <= 0 ? 3f : body.duration),
                    waitForResponse: false);
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/message/player" && req.HttpMethod == "POST")
            {
                var body = ReadJson<MessagePlayerBody>(req);
                LegacyRequest(CmdId.Request_InGameMessage_SinglePlayer,
                    new IdMsgPrio(body.playerId, body.text ?? "", (byte)body.prio, body.duration <= 0 ? 3f : body.duration),
                    waitForResponse: false);
                WriteJson(resp, new { ok = true });
                return;
            }

            if (path == "/chatbot/deliver" && req.HttpMethod == "POST")
            {
                var body = ReadJson<ChatbotDeliverBody>(req);
                var player = FindPlayerBySteamId(body?.steamId);
                if (player.HasValue && player.Value.IsOnline)
                    DeliverText(player.Value.EntityId, body.actionType, body.text ?? "", body.bannerPriority);
                WriteJson(resp, new { ok = true });
                return;
            }

            resp.StatusCode = 404;
            resp.Close();
        }

        void HandleGetStructures(HttpListenerRequest req, HttpListenerResponse resp)
        {
            var playfield = req.QueryString["playfield"];
            if (string.IsNullOrEmpty(playfield))
            {
                resp.StatusCode = 400;
                WriteJson(resp, new { error = "укажи ?playfield=<name>" });
                return;
            }

            var done = new ManualResetEventSlim(false);
            List<object> result = null;
            bool accepted = modApi.Application.GetStructures(playfield, null, null, structures =>
            {
                result = new List<object>();
                foreach (var s in structures)
                {
                    result.Add(new
                    {
                        id = s.id,
                        name = s.name,
                        type = s.type,
                        factionId = s.factionId,
                        factionGroup = s.factionGroup,
                        playfield = s.PlayfieldName,
                        pos = new { x = s.pos.x, y = s.pos.y, z = s.pos.z },
                        powered = s.powered,
                        fuel = s.fuel,
                        cntDevices = s.cntDevices,
                        cntBlocks = s.cntBlocks,
                        classNr = s.classNr,
                        coreType = s.coreType,
                        pilotId = s.pilotId,
                    });
                }
                done.Set();
            });

            if (!accepted || !done.Wait(TimeSpan.FromSeconds(10)))
            {
                resp.StatusCode = 504;
                WriteJson(resp, new { error = "GetStructures не ответил (плейфилд не загружен?)" });
                return;
            }
            WriteJson(resp, result);
        }

        void HandleGetAllStructures(HttpListenerResponse resp)
        {
            for (int attempt = 0; attempt < 5; attempt++)
            {
                try
                {
                    var list = (GlobalStructureList)LegacyRequest(CmdId.Request_GlobalStructure_List, null);
                    var result = new List<object>();
                    foreach (var kvp in list.globalStructures ?? new Dictionary<string, List<GlobalStructureInfo>>())
                    {
                        foreach (var s in kvp.Value)
                        {
                            result.Add(new
                            {
                                id = s.id,
                                name = s.name,
                                type = s.type,
                                factionId = s.factionId,
                                factionGroup = s.factionGroup,
                                playfield = string.IsNullOrEmpty(s.PlayfieldName) ? kvp.Key : s.PlayfieldName,
                            });
                        }
                    }
                    WriteJson(resp, result);
                    return;
                }
                catch (InvalidOperationException) when (attempt < 4)
                {
                }
            }
            resp.StatusCode = 503;
            WriteJson(resp, new { error = "структуры движка менялись во время чтения списка — попробуйте ещё раз" });
        }

        void HandleSetInventory(HttpListenerRequest req, HttpListenerResponse resp)
        {
            var body = ReadJson<SetInventoryBody>(req);
            LegacyRequest(CmdId.Request_Player_SetInventory, new Inventory
            {
                playerId = body.playerId,
                toolbelt = ToItemStacks(body.toolbelt),
                bag = ToItemStacks(body.bag),
            });
            WriteJson(resp, new { ok = true });
        }

        static List<object> ToDto(ItemStack[] stacks)
        {
            var result = new List<object>();
            if (stacks == null) return result;
            foreach (var s in stacks)
                result.Add(new { id = s.id, count = s.count, slotIdx = s.slotIdx, ammo = s.ammo, decay = s.decay });
            return result;
        }

        static ItemStack[] ToItemStacks(List<ItemStackDto> dtos)
        {
            if (dtos == null) return Array.Empty<ItemStack>();
            var result = new ItemStack[dtos.Count];
            for (int i = 0; i < dtos.Count; i++)
            {
                var d = dtos[i];
                result[i] = new ItemStack(d.id, d.count) { slotIdx = (byte)d.slotIdx, ammo = d.ammo, decay = d.decay };
            }
            return result;
        }

        void HandleTeleport(HttpListenerRequest req, HttpListenerResponse resp)
        {
            var body = ReadJson<TeleportBody>(req);
            var pos = new PVector3(body.x, body.y, body.z);
            var rot = new PVector3(body.rotX ?? 0, body.rotY ?? 0, body.rotZ ?? 0);

            if (!string.IsNullOrEmpty(body.playfield))
            {
                LegacyRequest(CmdId.Request_Player_ChangePlayerfield,
                    new IdPlayfieldPositionRotation { id = body.playerId, pos = pos, rot = rot, playfield = body.playfield });
            }
            else
            {
                LegacyRequest(CmdId.Request_Entity_Teleport,
                    new IdPositionRotation { id = body.playerId, pos = pos, rot = rot });
            }
            WriteJson(resp, new { ok = true });
        }

        void HandleSetPlayerInfo(HttpListenerRequest req, HttpListenerResponse resp)
        {
            var body = ReadJson<SetPlayerInfoBody>(req);
            LegacyRequest(CmdId.Request_Player_SetPlayerInfo, new PlayerInfoSet
            {
                entityId = body.playerId,
                health = body.health,
                healthMax = body.healthMax,
                stamina = body.stamina,
                staminaMax = body.staminaMax,
                food = body.food,
                foodMax = body.foodMax,
                oxygen = body.oxygen,
                oxygenMax = body.oxygenMax,
                radiation = body.radiation,
                radiationMax = body.radiationMax,
                bodyTemp = body.bodyTemp,
                bodyTempMax = body.bodyTempMax,
                experiencePoints = body.experiencePoints,
                upgradePoints = body.upgradePoints,
                factionGroup = body.factionGroup,
                factionId = body.factionId,
                factionRole = body.factionRole,
            });
            WriteJson(resp, new { ok = true });
        }

        void HandleSpawnStructure(HttpListenerRequest req, HttpListenerResponse resp)
        {
            var body = ReadJson<StructureSpawnBody>(req);
            var fromExport = !string.IsNullOrEmpty(body.exportedEntityDatBase64);
            if (string.IsNullOrEmpty(body.epbBase64) && !fromExport)
            {
                resp.StatusCode = 400;
                WriteJson(resp, new { error = "epbBase64 or exportedEntityDatBase64 required" });
                return;
            }
            var typeByte = StructureTypeByte(body.type);
            if (typeByte == 0)
            {
                resp.StatusCode = 400;
                WriteJson(resp, new { error = "type must be BA/CV/SV/HV/AstVoxel" });
                return;
            }

            var playfield = body.playfield;
            var pos = new PVector3(body.x ?? 0, body.y ?? 0, body.z ?? 0);
            byte factionGroup = 0;
            var factionId = 0;

            if (body.ownerEntityId.HasValue)
            {
                var pi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(body.ownerEntityId.Value));
                if (string.IsNullOrEmpty(playfield)) playfield = pi.playfield;
                if (!body.x.HasValue && !body.y.HasValue && !body.z.HasValue)
                    pos = new PVector3(pi.pos.x, pi.pos.y + 5, pi.pos.z); 
                factionGroup = 1; 
                factionId = body.ownerEntityId.Value;
            }
            if (body.factionId.HasValue)
            {
                factionGroup = 0; 
                factionId = body.factionId.Value;
            }
            if (body.spawnNearPlayerId.HasValue && !body.x.HasValue && !body.y.HasValue && !body.z.HasValue)
            {
                var nearPi = (PlayerInfo)LegacyRequest(CmdId.Request_Player_Info, new Id(body.spawnNearPlayerId.Value));
                playfield = nearPi.playfield;
                pos = new PVector3(nearPi.pos.x, nearPi.pos.y + 5, nearPi.pos.z);
            }

            if (string.IsNullOrEmpty(playfield))
            {
                resp.StatusCode = 400;
                WriteJson(resp, new { error = "playfield required (or ownerEntityId to infer it)" });
                return;
            }

            var dir = Path.Combine(modApi.Application.GetPathFor(AppFolder.SaveGame), "Mods", "ElbMod", "SpawnBlueprints");
            Directory.CreateDirectory(dir);
            var prefabName = "elb_spawn_" + Guid.NewGuid().ToString("N").Substring(0, 8);
            var exportedEntityDatPath = fromExport ? Path.Combine(dir, prefabName + ".dat") : null;
            if (fromExport)
                File.WriteAllBytes(exportedEntityDatPath, Convert.FromBase64String(body.exportedEntityDatBase64));
            else
                File.WriteAllBytes(Path.Combine(dir, prefabName + ".epb"), Convert.FromBase64String(body.epbBase64));

            try
            {
                var newId = (Id)LegacyRequest(CmdId.Request_NewEntityId, null);
                try { LegacyRequest(CmdId.Request_Load_Playfield, new PlayfieldLoad(20, playfield, 0)); }
                catch {  }

                LegacyRequest(CmdId.Request_Entity_Spawn, new EntitySpawnInfo
                {
                    forceEntityId = newId.id,
                    playfield = playfield,
                    pos = pos,
                    rot = new PVector3(body.rotX, body.rotY, body.rotZ),
                    name = string.IsNullOrEmpty(body.name) ? prefabName : body.name,
                    type = typeByte,
                    entityTypeName = "",
                    prefabName = fromExport ? "" : prefabName,
                    prefabDir = fromExport ? "" : dir,
                    exportedEntityDat = fromExport ? exportedEntityDatPath : null,
                    factionGroup = factionGroup,
                    factionId = factionId,
                });
                try { LegacyRequest(CmdId.Request_Structure_Touch, newId); }
                catch (Exception ex) { modApi.LogError("ElbMod: /structures/spawn: touch: " + ex); }

                WriteJson(resp, new { ok = true, id = newId.id, playfield });
            }
            catch (Exception ex)
            {
                modApi.LogError("ElbMod: /structures/spawn: " + ex);
                resp.StatusCode = 500;
                WriteJson(resp, new { error = ex.Message });
            }
            finally
            {
                if (fromExport) { try { File.Delete(exportedEntityDatPath); } catch { } }
            }
        }


        class SendChatRequest
        {
            public string text { get; set; }
            public string channel { get; set; }
            public string factionId { get; set; }
            public string allianceFactionIds { get; set; }
            public string asSteamId { get; set; }
            public string recipientSteamId { get; set; }
            public string senderName { get; set; }
        }

        global::Eleon.Modding.PlayerData? FindPlayerBySteamId(string steamId)
        {
            foreach (var id in modApi.Application.GetPlayerEntityIds())
            {
                var pd = modApi.Application.GetPlayerDataFor(id);
                if (pd.HasValue && pd.Value.SteamId == steamId) return pd;
            }
            return null;
        }

        global::Eleon.MsgChannel ParseChannel(string channel)
        {
            switch ((channel ?? "").Trim().ToLowerInvariant())
            {
                case "faction": return global::Eleon.MsgChannel.Faction;
                case "alliance": return global::Eleon.MsgChannel.Alliance;
                case "server": return global::Eleon.MsgChannel.Server;
                default: return global::Eleon.MsgChannel.Global;
            }
        }
        class IdBody { public int id { get; set; } public string playfield { get; set; } }
        class StructureFactionBody { public int id { get; set; } public string abbrev { get; set; } }
        class StructureRepairBody { public int id { get; set; } public string playfield { get; set; } }
        class StructureMoveBody { public int id { get; set; } public float x { get; set; } public float y { get; set; } public float z { get; set; } }
        class StructureSpawnBody
        {
            public string playfield { get; set; }
            public float? x { get; set; }
            public float? y { get; set; }
            public float? z { get; set; }
            public float rotX { get; set; }
            public float rotY { get; set; }
            public float rotZ { get; set; }
            public string type { get; set; } 
            public string name { get; set; }
            public int? ownerEntityId { get; set; }
            public int? factionId { get; set; }
            public string epbBase64 { get; set; }
            public string exportedEntityDatBase64 { get; set; }
            public int? spawnNearPlayerId { get; set; }
        }
        class MessageAllBody { public string text { get; set; } public int prio { get; set; } public float duration { get; set; } }
        class MessagePlayerBody { public int playerId { get; set; } public string text { get; set; } public int prio { get; set; } public float duration { get; set; } }
        class ChatbotDeliverBody { public string steamId { get; set; } public string actionType { get; set; } public string text { get; set; } public int bannerPriority { get; set; } }
        class ItemStackDto { public int id { get; set; } public int count { get; set; } public int slotIdx { get; set; } public int ammo { get; set; } public int decay { get; set; } }
        class SetInventoryBody { public int playerId { get; set; } public List<ItemStackDto> toolbelt { get; set; } public List<ItemStackDto> bag { get; set; } }
        class AddItemBody { public int playerId { get; set; } public int itemId { get; set; } public int count { get; set; } }
        class TeleportBody { public int playerId { get; set; } public float x { get; set; } public float y { get; set; } public float z { get; set; } public string playfield { get; set; } public float? rotX { get; set; } public float? rotY { get; set; } public float? rotZ { get; set; } }

        class SetPlayerInfoBody
        {
            public int playerId { get; set; }
            public int? health { get; set; }
            public int? healthMax { get; set; }
            public int? stamina { get; set; }
            public int? staminaMax { get; set; }
            public int? food { get; set; }
            public int? foodMax { get; set; }
            public int? oxygen { get; set; }
            public int? oxygenMax { get; set; }
            public int? radiation { get; set; }
            public int? radiationMax { get; set; }
            public int? bodyTemp { get; set; }
            public int? bodyTempMax { get; set; }
            public int? experiencePoints { get; set; }
            public int? upgradePoints { get; set; }
            public byte? factionGroup { get; set; }
            public int? factionId { get; set; }
            public byte? factionRole { get; set; }
        }
        class AddCreditsBody { public int playerId { get; set; } public double amount { get; set; } }


        static T ReadJson<T>(HttpListenerRequest req)
        {
            using var reader = new StreamReader(req.InputStream, req.ContentEncoding ?? Encoding.UTF8);
            return JsonSerializer.Deserialize<T>(reader.ReadToEnd());
        }

        void WriteJson(HttpListenerResponse resp, object data)
        {
            var bytes = Encoding.UTF8.GetBytes(JsonSerializer.Serialize(data));
            resp.ContentType = "application/json";
            resp.ContentLength64 = bytes.Length;
            resp.OutputStream.Write(bytes, 0, bytes.Length);
            resp.OutputStream.Close();
        }
    }
}
