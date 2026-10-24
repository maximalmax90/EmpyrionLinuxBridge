var historyAnchorKind = '', historyAnchorKey = '', historyRawEvents = [];
var historyAnchorTargets = null;
var historyFactionNames = {}, historyPlayerNames = {}, historyPlayerFaction = {}, historyStructureFaction = {}, historyPlayfieldSystem = {};
var historyPage = 0, historyTotal = 0;

function historyPad(n) { return (n < 10 ? '0' : '') + n; }

function historyToLocalInput(d) {
  return d.getFullYear() + '-' + historyPad(d.getMonth() + 1) + '-' + historyPad(d.getDate()) +
    'T' + historyPad(d.getHours()) + ':' + historyPad(d.getMinutes());
}

function historyEscapeHtml(s) {
  var div = document.createElement('div');
  div.textContent = s == null ? '' : String(s);
  return div.innerHTML;
}

function historyUniqueSorted(values) {
  var seen = {}, out = [];
  values.forEach(function (v) {
    if (v && !seen[v]) { seen[v] = true; out.push(v); }
  });
  out.sort();
  return out;
}

function historyCellClass(base, highlighted) {
  var classes = [];
  if (base) classes.push(base);
  if (highlighted) classes.push('hist-col-highlight');
  return classes.length ? ' class="' + classes.join(' ') + '"' : '';
}

var HISTORY_COLUMNS = [
  { key: 'credits', i18n: 'historyColCredits', numeric: true },
  { key: 'kills', i18n: 'historyColKills', numeric: true },
  { key: 'died', i18n: 'historyColDied', numeric: true },
  { key: 'exp', i18n: 'historyColExp', numeric: true },
  { key: 'factionId', i18n: 'historyColFactionId', numeric: false },
  { key: 'factionGroup', i18n: 'historyColFactionGroup', numeric: false },
  { key: 'cntBlocks', i18n: 'historyColBlocks', numeric: true },
  { key: 'cntDevices', i18n: 'historyColDevices', numeric: true },
  { key: 'cntTriangles', i18n: 'historyColTriangles', numeric: true },
  { key: 'cntLights', i18n: 'historyColLights', numeric: true },
  { key: 'powered', i18n: 'historyColPowered', numeric: false },
  { key: 'fuel', i18n: 'historyColFuel', numeric: true },
];
var HISTORY_DELTA_COLUMNS = HISTORY_COLUMNS.filter(function (c) { return c.numeric; });

var historyPendingFilters = null;

function openHistoryModalFilter(filters) {
  historyPendingFilters = filters || null;
  openHistoryModal('', null, null);
}

function quickLinkStructures(filterKey, value) {
  var filters = {};
  filters[filterKey] = value;
  openStructuresModal(filters);
}

function openHistoryModal(kind, key, name) {
  document.getElementById('modal-title').textContent = I18N.historyTitle;
  document.getElementById('modal-box').classList.add('fullscreen');
  document.getElementById('modal-body').innerHTML = historyFiltersHtml();

  var confirmBtn = document.getElementById('modal-confirm-btn');
  if (confirmBtn) confirmBtn.style.display = 'none';
  var modalActions = document.querySelector('.modal-actions');
  if (modalActions) modalActions.style.display = 'none';
  showModal();

  historySetup(kind, key, name);
}

function initHistoryPage(containerId) {
  document.getElementById(containerId).innerHTML = historyFiltersHtml();
  historySetup('', null, null);
}

function historySetup(kind, key, name) {
  historyAnchorKind = kind; historyAnchorKey = key;
  historyPage = 0; historyTotal = 0; 

  if (kind) {
    document.getElementById('hist-entity-kind').value = kind;
    var entInput = document.getElementById('hist-entity-value');
    entInput.value = name || '';
    entInput.style.display = '';
    entInput.placeholder = kind === 'player' ? I18N.historyEntityValueAllPlayers : I18N.historyEntityValueAllStructures;
  }
  updateHistoryRadiusVisibility();

  if (historyPendingFilters) {
    if (historyPendingFilters.system) document.getElementById('hist-filter-system').value = historyPendingFilters.system;
    if (historyPendingFilters.playfield) document.getElementById('hist-filter-playfield').value = historyPendingFilters.playfield;
  }

  historyFactionNames = {}; historyPlayerNames = {}; historyPlayerFaction = {}; historyStructureFaction = {}; historyPlayfieldSystem = {};
  fetch('/action/history/names').then(function (r) { return r.json(); }).then(function (data) {
    historyFactionNames = data.factionNames || {};
    historyPlayerNames = data.playerNames || {};
    historyPlayerFaction = data.playerFaction || {};
    historyStructureFaction = data.structureFaction || {};
    historyPlayfieldSystem = data.playfieldSystem || {};
    historyPopulatePlayfieldSystemDatalists();
    renderHistoryResults(); 
  }).catch(function () {});

  historyAnchorTargets = null;
  fetch('/action/warp/targets').then(function (r) { return r.json(); }).then(function (data) {
    historyAnchorTargets = data;
    historySetupEntityPicker(kind, key);
  }).catch(function () {});

  runHistoryQuery();
}

function historyFiltersHtml() {
  var now = new Date();
  var weekAgo = new Date(now.getTime() - 7 * 24 * 3600 * 1000);
  var headCols = HISTORY_COLUMNS.map(function (c) { return '<th data-field="' + c.key + '">' + I18N[c.i18n] + '</th>'; }).join('');
  var deltaFieldOptions = '<option value="">' + I18N.historyDeltaAny + '</option>' +
    HISTORY_COLUMNS.map(function (c) { return '<option value="' + c.key + '">' + I18N[c.i18n] + '</option>'; }).join('');

  return '<div class="hist-filter-row">' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupRange + '</legend><div class="field-grid">' +
      '<label>' + I18N.historyFrom + '<input type="datetime-local" id="hist-from" value="' + historyToLocalInput(weekAgo) + '"></label>' +
      '<label>' + I18N.historyTo + '<input type="datetime-local" id="hist-to" value="' + historyToLocalInput(now) + '"></label>' +
      '<label id="hist-radius-wrap" style="display:none">' + I18N.historyDistance + '<input type="number" id="hist-distance" value="1000"></label>' +
    '</div></fieldset>' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupAnchor + '</legend><div class="field-grid">' +
      '<label>' + I18N.historyEntityKindLabel +
        '<select id="hist-entity-kind" onchange="onHistoryEntityKindChange()">' +
          '<option value="">' + I18N.historyEntityKindAll + '</option>' +
          '<option value="player">' + I18N.historyKindPlayer + '</option>' +
          '<option value="structure">' + I18N.historyKindStructure + '</option>' +
        '</select>' +
      '</label>' +
      '<label id="hist-entity-value-wrap" style="display:none">' + I18N.historyColName +
        '<input type="text" id="hist-entity-value" list="hist-entity-datalist" onchange="onHistoryEntityValueChange()">' +
      '</label>' +
      '<datalist id="hist-entity-datalist"></datalist>' +
      '<label>' + I18N.historyFilterFactionLabel +
        '<select id="hist-filter-faction"><option value="">' + I18N.historyFilterAllFactions + '</option></select>' +
      '</label>' +
      '<label>' + I18N.historySystemLabel +
        '<input type="text" id="hist-filter-system" list="hist-filter-system-datalist" placeholder="' + I18N.historyFilterAllSystems + '">' +
      '</label>' +
      '<datalist id="hist-filter-system-datalist"></datalist>' +
      '<label>' + I18N.historyColPlayfield +
        '<input type="text" id="hist-filter-playfield" list="hist-filter-playfield-datalist" placeholder="' + I18N.historyFilterAllPlayfields + '">' +
      '</label>' +
      '<datalist id="hist-filter-playfield-datalist"></datalist>' +
    '</div></fieldset>' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupExtra + '</legend><div class="field-grid">' +
      '<div class="checkbox-list">' +
        '<label><input type="checkbox" id="hist-hide-onlypos" checked> ' + I18N.historyHideOnlyPos + '</label>' +
        '<label><input type="checkbox" id="hist-only-changes" checked> ' + I18N.historyFilterOnlyChanges + '</label>' +
      '</div>' +
      '<label>' + I18N.historyLimitLabel + '<input type="number" id="hist-limit" value="200" min="1"></label>' +
    '</div></fieldset>' +
    '</div>' +
    '<div class="hist-filter-row">' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupEventTypes + '</legend><div class="checkbox-list">' +
      '<label><input type="checkbox" id="hist-show-players" checked onchange="updateHistoryGroupVisibility()"> ' + I18N.historyFilterPlayers + '</label>' +
      '<label><input type="checkbox" id="hist-show-structures" checked onchange="updateHistoryGroupVisibility()"> ' + I18N.historyFilterStructures + '</label>' +
    '</div></fieldset>' +
    '<fieldset class="player-group" id="hist-group-players"><legend>' + I18N.historyFilterPlayersGroup + '</legend><div class="checkbox-grid">' +
      '<label><input type="checkbox" id="hist-show-player-regular" checked> ' + I18N.historyFilterRegular + '</label>' +
      '<label><input type="checkbox" id="hist-show-char-creation"> ' + I18N.historyFilterCharCreation + '</label>' +
      '<label><input type="checkbox" id="hist-show-went-online" checked> ' + I18N.historyFilterWentOnline + '</label>' +
      '<label><input type="checkbox" id="hist-show-went-offline" checked> ' + I18N.historyFilterWentOffline + '</label>' +
    '</div></fieldset>' +
    '<fieldset class="player-group" id="hist-group-structures"><legend>' + I18N.historyFilterStructuresGroup + '</legend><div class="checkbox-grid">' +
      '<label><input type="checkbox" id="hist-show-structure-regular" checked> ' + I18N.historyFilterRegular + '</label>' +
      '<label><input type="checkbox" id="hist-show-new-structures"> ' + I18N.historyFilterNewStructures + '</label>' +
      '<label><input type="checkbox" id="hist-show-structure-deleted" checked> ' + I18N.historyFilterStructureDeleted + '</label>' +
    '</div></fieldset>' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupDelta + '</legend><div class="field-grid">' +
      '<label><span class="invisible-label">&nbsp;</span><select id="hist-delta-field">' + deltaFieldOptions + '</select></label>' +
      '<label><span class="invisible-label">&nbsp;</span>' +
        '<div class="stat-pair">' +
          '<select id="hist-delta-op"><option value="gt">&gt;</option><option value="lt">&lt;</option></select>' +
          '<input type="number" id="hist-delta-value">' +
        '</div>' +
      '</label>' +
    '</div></fieldset>' +
    '</div>' +
    '<div class="hist-run-row">' +
      '<button type="button" class="btn-small btn-primary" onclick="runHistoryQuery()">' + I18N.historyShow + '</button>' +
    '</div>' +
    '<div class="table-scroll"><table class="structures-table history-table"><thead><tr>' +
      '<th>' + I18N.historyColTime + '</th><th>' + I18N.historyColKind + '</th><th>' + I18N.historyColName + '</th>' +
      '<th>' + I18N.historyColPlayfield + '</th><th>' + I18N.historyColDistance + '</th>' + headCols +
    '</tr></thead><tbody id="hist-tbody"></tbody></table></div>' +
    '<div class="hist-actions">' +
      '<span id="hist-page-info" class="dim"></span>' +
      '<button type="button" class="btn-small nfi nfi-chevron-left" id="hist-page-prev" onclick="historyChangePage(-1)"></button>' +
      '<button type="button" class="btn-small nfi nfi-chevron-right" id="hist-page-next" onclick="historyChangePage(1)"></button>' +
    '</div>';
}

function historySetupEntityPicker(kind, key) {
  document.getElementById('hist-entity-kind').value = kind || '';
  historyPopulateEntityDatalist(kind);
  if (!kind) return;
  var items = historyAnchorItems(kind);
  var match = items.filter(function (it) { return historyAnchorItemKey(kind, it) === key; })[0];
  if (match) document.getElementById('hist-entity-value').value = historyAnchorItemLabel(kind, match);
}

function historyAnchorItems(kind) {
  if (!historyAnchorTargets) return [];
  return kind === 'player' ? (historyAnchorTargets.players || []) : (historyAnchorTargets.structures || []);
}
function historyAnchorItemKey(kind, it) {
  return kind === 'player' ? it.steamId : String(it.id);
}
function historyAnchorItemLabel(kind, it) {
  return kind === 'player' ? it.name : (it.name + ' #' + it.id);
}

function historyPopulateEntityDatalist(kind) {
  var dl = document.getElementById('hist-entity-datalist');
  if (!dl) return;
  var items = kind ? historyAnchorItems(kind) : [];
  dl.innerHTML = items.map(function (it) {
    return '<option value="' + historyEscapeHtml(historyAnchorItemLabel(kind, it)) + '">';
  }).join('');
}

function historyResolveEntityKey(kind, text) {
  if (!kind || !text) return null;
  var items = historyAnchorItems(kind);
  var byKey = items.filter(function (it) { return historyAnchorItemKey(kind, it) === text; })[0];
  if (byKey) return byKey;
  return items.filter(function (it) { return historyAnchorItemLabel(kind, it) === text; })[0] || null;
}

function updateHistoryRadiusVisibility() {
  var wrap = document.getElementById('hist-radius-wrap');
  if (wrap) wrap.style.display = historyAnchorKey ? 'contents' : 'none';
}

function historyPopulatePlayfieldSystemDatalists() {
  var playfields;
  if (historyAnchorKey) {
    playfields = historyUniqueSorted(historyRawEvents.filter(function (e) {
      return e.kind === historyAnchorKind && e.entityKey === historyAnchorKey;
    }).map(function (e) { return e.playfield; }));
  } else {
    playfields = historyUniqueSorted(Object.keys(historyPlayfieldSystem));
  }
  var systems = historyUniqueSorted(playfields.map(function (pf) { return historyPlayfieldSystem[pf]; }));
  var sysDl = document.getElementById('hist-filter-system-datalist');
  var pfDl = document.getElementById('hist-filter-playfield-datalist');
  if (sysDl) sysDl.innerHTML = systems.map(function (s) { return '<option value="' + historyEscapeHtml(s) + '">'; }).join('');
  if (pfDl) pfDl.innerHTML = playfields.map(function (p) { return '<option value="' + historyEscapeHtml(p) + '">'; }).join('');
}


function updateHistoryGroupVisibility() {
  var groupPlayers = document.getElementById('hist-group-players');
  var groupStructures = document.getElementById('hist-group-structures');
  var showPlayersEl = document.getElementById('hist-show-players');
  var showStructuresEl = document.getElementById('hist-show-structures');
  if (groupPlayers && showPlayersEl) groupPlayers.style.display = showPlayersEl.checked ? '' : 'none';
  if (groupStructures && showStructuresEl) groupStructures.style.display = showStructuresEl.checked ? '' : 'none';
}

function onHistoryEntityKindChange() {
  var kind = document.getElementById('hist-entity-kind').value;
  var input = document.getElementById('hist-entity-value');
  var wrap = document.getElementById('hist-entity-value-wrap');
  input.value = '';
  if (kind) {
    wrap.style.display = '';
    input.placeholder = kind === 'player' ? I18N.historyEntityValueAllPlayers : I18N.historyEntityValueAllStructures;
  } else {
    wrap.style.display = 'none';
  }
  historyPopulateEntityDatalist(kind);
  historyAnchorKind = ''; historyAnchorKey = '';
  updateHistoryRadiusVisibility();
}

function onHistoryEntityValueChange() {
  var kind = document.getElementById('hist-entity-kind').value;
  var text = document.getElementById('hist-entity-value').value.trim();
  var match = historyResolveEntityKey(kind, text);
  historyAnchorKind = match ? kind : '';
  historyAnchorKey = match ? historyAnchorItemKey(kind, match) : '';
  updateHistoryRadiusVisibility();
}

function historyColspan() { return 5 + HISTORY_COLUMNS.length; }

function runHistoryQuery() {
  historyPage = 0;
  historyFetchPage();
}

function historyChangePage(delta) {
  var next = historyPage + delta;
  if (next < 0) return;
  if (delta > 0 && next * historyCurrentLimit() >= historyTotal) return;
  historyPage = next;
  historyFetchPage();
}

function historyCurrentLimit() {
  return parseInt(document.getElementById('hist-limit').value, 10) || 1000;
}

function historyFetchPage() {
  var fromEl = document.getElementById('hist-from'), toEl = document.getElementById('hist-to');
  if (!fromEl || !toEl) return;
  var limit = historyCurrentLimit();
  var body = {
    kind: historyAnchorKey ? historyAnchorKind : '',
    key: historyAnchorKey || '',
    eventKind: historyAnchorKey ? '' : document.getElementById('hist-entity-kind').value,
    playfield: document.getElementById('hist-filter-playfield').value || '',
    from: new Date(fromEl.value).toISOString(),
    to: new Date(toEl.value).toISOString(),
    distance: parseFloat(document.getElementById('hist-distance').value) || 1000,
    hideFirst: false,
    hideOnlyPosition: document.getElementById('hist-hide-onlypos').checked,
    limit: limit,
    offset: historyPage * limit,
  };
  var tbody = document.getElementById('hist-tbody');
  tbody.innerHTML = '<tr><td colspan="' + historyColspan() + '" class="dim">' + I18N.historyLoading + '</td></tr>';
  fetch('/action/history/around', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  }).then(function (r) { return r.json(); }).then(function (data) {
    historyRawEvents = (data && data.events) || [];
    historyTotal = (data && data.total) || 0;
    historyBuildCarriedValues();
    historyPopulateFilterSelect(document.getElementById('hist-filter-faction'),
      historyUniqueSorted(historyRawEvents.map(historyEventFaction)), I18N.historyFilterAllFactions);
    if (historyPendingFilters && historyPendingFilters.faction) {
      var factionSelect = document.getElementById('hist-filter-faction');
      var hasOpt = Array.prototype.some.call(factionSelect.options, function (o) { return o.value === historyPendingFilters.faction; });
      if (!hasOpt) {
        var opt = document.createElement('option');
        opt.value = historyPendingFilters.faction; opt.textContent = historyPendingFilters.faction;
        factionSelect.appendChild(opt);
      }
      factionSelect.value = historyPendingFilters.faction;
    }
    historyPendingFilters = null;
    historyPopulatePlayfieldSystemDatalists(); 
    historyRenderPager(limit);
    renderHistoryResults();
  }).catch(function () {
    tbody.innerHTML = '<tr><td colspan="' + historyColspan() + '" class="dim">' + I18N.requestError + '</td></tr>';
  });
}

function historyRenderPager(limit) {
  var pages = historyTotal > 0 ? Math.ceil(historyTotal / limit) : 1;
  var info = document.getElementById('hist-page-info');
  var prevBtn = document.getElementById('hist-page-prev');
  var nextBtn = document.getElementById('hist-page-next');
  if (!info || !prevBtn || !nextBtn) return;
  info.textContent = I18N.historyPageInfo
    .replace('{page}', String(historyPage + 1))
    .replace('{pages}', String(pages))
    .replace('{total}', String(historyTotal));
  prevBtn.disabled = historyPage <= 0;
  nextBtn.disabled = (historyPage + 1) * limit >= historyTotal;
}

function historyBuildCarriedValues() {
  var state = {}; 
  historyRawEvents.forEach(function (e) {
    var entState = state[e.entityKey] || (state[e.entityKey] = {});
    var carried = {};
    HISTORY_COLUMNS.forEach(function (c) {
      if (entState.hasOwnProperty(c.key)) carried[c.key] = entState[c.key];
    });
    e._carried = carried;
    var ch = e.changed || {};
    HISTORY_COLUMNS.forEach(function (c) {
      if (ch[c.key] && ch[c.key].new !== undefined) entState[c.key] = ch[c.key].new;
    });
  });
}

function historyPopulateFilterSelect(select, values, allLabel) {
  var prev = select.value;
  select.innerHTML = '<option value="">' + allLabel + '</option>' +
    values.map(function (v) { return '<option value="' + historyEscapeHtml(v) + '">' + historyEscapeHtml(v) + '</option>'; }).join('');
  if (values.indexOf(prev) !== -1) select.value = prev;
}

function historyEventFaction(e) {
  return e.kind === 'player' ? historyPlayerFaction[e.entityKey] : historyStructureFaction[e.entityKey];
}

function historyResolveCellValue(key, val) {
  if (val === null || val === undefined) return val;
  if (key === 'factionId') {
    var id = String(val);
    return historyFactionNames[id] || historyPlayerNames[id] || val;
  }
  if (key === 'factionGroup') {
    if (val === 0) return I18N.historyFactionGroupFaction;
    if (val === 1) return I18N.historyFactionGroupPlayer;
    return I18N.historyFactionGroupNpc + ' (' + val + ')';
  }
  return val;
}

function historyPlayerCategory(e) {
  if (e.isFirst) return 'creation';
  var onlineCh = (e.changed || {}).online;
  if (onlineCh && onlineCh.new === true) return 'online';
  if (onlineCh && onlineCh.new === false) return 'offline';
  return 'regular';
}
function historyStructureCategory(e) {
  if (e.isFirst) return 'new';
  if (e.isDeleted) return 'deleted';
  return 'regular';
}

function historyRowClass(e) {
  if (e.kind === 'player') {
    var cat = historyPlayerCategory(e);
    if (cat === 'creation') return 'hist-row-new';
    if (cat === 'online') return 'hist-row-online';
    if (cat === 'offline') return 'hist-row-offline';
  } else if (e.kind === 'structure') {
    var structCat = historyStructureCategory(e);
    if (structCat === 'new') return 'hist-row-new';
    if (structCat === 'deleted') return 'hist-row-deleted';
  }
  return '';
}

function historyHasRealDelta(ch) {
  return ch && ch.old !== null && ch.old !== undefined;
}

function historyDeltaBadge(key, ch) {
  if (!historyHasRealDelta(ch)) return '';
  if (!HISTORY_DELTA_COLUMNS.some(function (c) { return c.key === key; })) return '';
  var oldNum = Number(ch.old), newNum = Number(ch.new);
  if (isNaN(oldNum) || isNaN(newNum)) return '';
  var delta = newNum - oldNum;
  if (delta === 0) return '';
  var cls = delta > 0 ? 'pos' : 'neg';
  var sign = delta > 0 ? '+' : '';
  return '<span class="delta-badge ' + cls + '">' + sign + delta + '</span>';
}

function historyDeltaFilterPass(e, field, op, threshold) {
  if (e.isFirst) return false;
  var keys = field ? [field] : HISTORY_DELTA_COLUMNS.map(function (c) { return c.key; });
  return keys.some(function (k) {
    var ch = (e.changed || {})[k];
    if (!historyHasRealDelta(ch)) return false;
    var delta = Number(ch.new) - Number(ch.old);
    if (isNaN(delta)) return false;
    return op === 'lt' ? delta < threshold : delta > threshold;
  });
}

function historyFieldChangedFilterPass(e, field) {
  if (e.isFirst) return false;
  return historyHasRealDelta((e.changed || {})[field]);
}

function renderHistoryResults() {
  var tbody = document.getElementById('hist-tbody');
  if (!tbody) return;
  var showPlayers = document.getElementById('hist-show-players').checked;
  var showPlayerRegular = document.getElementById('hist-show-player-regular').checked;
  var showCharCreation = document.getElementById('hist-show-char-creation').checked;
  var showWentOnline = document.getElementById('hist-show-went-online').checked;
  var showWentOffline = document.getElementById('hist-show-went-offline').checked;
  var showStructures = document.getElementById('hist-show-structures').checked;
  var showStructureRegular = document.getElementById('hist-show-structure-regular').checked;
  var showNewStructures = document.getElementById('hist-show-new-structures').checked;
  var showStructureDeleted = document.getElementById('hist-show-structure-deleted').checked;
  var playfieldFilter = document.getElementById('hist-filter-playfield').value;
  var systemFilter = document.getElementById('hist-filter-system').value;
  var factionFilter = document.getElementById('hist-filter-faction').value;
  var onlyChanges = document.getElementById('hist-only-changes').checked;
  var showCarryForward = !onlyChanges;
  var deltaField = document.getElementById('hist-delta-field').value;
  var deltaOp = document.getElementById('hist-delta-op').value;
  var deltaValueRaw = document.getElementById('hist-delta-value').value;
  var deltaThreshold = deltaValueRaw === '' ? null : parseFloat(deltaValueRaw);

  updateHistoryGroupVisibility();

  document.querySelectorAll('.history-table thead th[data-field]').forEach(function (th) {
    th.classList.toggle('hist-col-highlight', !!deltaField && th.getAttribute('data-field') === deltaField);
  });

  var events = historyRawEvents.filter(function (e) {
    if (e.kind === 'player') {
      if (!showPlayers) return false;
      var pcat = historyPlayerCategory(e);
      if (pcat === 'regular' && !showPlayerRegular) return false;
      if (pcat === 'creation' && !showCharCreation) return false;
      if (pcat === 'online' && !showWentOnline) return false;
      if (pcat === 'offline' && !showWentOffline) return false;
    } else if (e.kind === 'structure') {
      if (!showStructures) return false;
      var scat = historyStructureCategory(e);
      if (scat === 'regular' && !showStructureRegular) return false;
      if (scat === 'new' && !showNewStructures) return false;
      if (scat === 'deleted' && !showStructureDeleted) return false;
    }
    if (playfieldFilter && e.playfield !== playfieldFilter) return false;
    if (systemFilter && historyPlayfieldSystem[e.playfield] !== systemFilter) return false;
    if (factionFilter && historyEventFaction(e) !== factionFilter) return false;
    if (deltaField && deltaThreshold === null) {
      if (!historyFieldChangedFilterPass(e, deltaField)) return false;
    } else if (deltaThreshold !== null && !isNaN(deltaThreshold)) {
      if (!historyDeltaFilterPass(e, deltaField, deltaOp, deltaThreshold)) return false;
    }
    return true;
  });

  tbody.innerHTML = '';
  if (events.length === 0) {
    tbody.innerHTML = '<tr><td colspan="' + historyColspan() + '" class="dim">' + I18N.historyEmpty + '</td></tr>';
    return;
  }
  events.forEach(function (e) {
    var typeParts = [];
    if (e.isFirst) typeParts.push(I18N.historyNew);
    if (e.isDeleted) typeParts.push(I18N.historyDeleted);
    var kindLabel = e.kind === 'player' ? I18N.historyKindPlayer : I18N.historyKindStructure;
    if (typeParts.length) kindLabel += ' (' + typeParts.join(', ') + ')';

    var cells = HISTORY_COLUMNS.map(function (c) {
      var highlighted = !!deltaField && c.key === deltaField;
      var ch = (e.changed || {})[c.key];
      if (ch) {
        var badge = historyDeltaBadge(c.key, ch);
        var oldText = historyHasRealDelta(ch) ? historyEscapeHtml(historyResolveCellValue(c.key, ch.old)) : '?';
        var newText = historyEscapeHtml(historyResolveCellValue(c.key, ch.new));
        return '<td' + historyCellClass('', highlighted) + '>' + badge + oldText + '<span class="nfi nfi-inline nfi-arrow-right"></span>' + newText + '</td>';
      }
      if (showCarryForward && e._carried && e._carried.hasOwnProperty(c.key)) {
        return '<td' + historyCellClass('dim', highlighted) + '>' + historyEscapeHtml(historyResolveCellValue(c.key, e._carried[c.key])) + '</td>';
      }
      return '<td' + historyCellClass('', highlighted) + '></td>';
    }).join('');

    var tr = document.createElement('tr');
    var rowClass = historyRowClass(e);
    if (rowClass) tr.className = rowClass;
    tr.innerHTML =
      '<td>' + historyEscapeHtml(new Date(e.ts).toLocaleString()) + '</td>' +
      '<td>' + historyEscapeHtml(kindLabel) + '</td>' +
      '<td>' + historyEscapeHtml(e.name) + '</td>' +
      '<td>' + historyEscapeHtml(e.playfield) + '</td>' +
      '<td>' + (historyAnchorKey ? e.distance.toFixed(1) : '—') + '</td>' +
      cells;
    tbody.appendChild(tr);
  });
}
