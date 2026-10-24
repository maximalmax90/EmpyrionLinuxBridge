
function structAction(path, id, extra) {
  var body = new URLSearchParams(extra || {});
  fetch('/action/structures/' + id + path, { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: body.toString() })
    .then(function (r) { return r.text(); }).then(showToastHTML);
}
function touchStructure(id, playfield) { structAction('/touch', id, { playfield: playfield }); }
function repairStructure(id, playfield) { structAction('/repair', id, { playfield: playfield }); }
function backupStructureNow(id, btn) {
  if (btn) btn.disabled = true;
  var body = new URLSearchParams();
  fetch('/action/structures/' + id + '/backup-now', { method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: body.toString() })
    .then(function (r) { return r.text(); })
    .then(function (html) { if (btn) btn.disabled = false; showToastHTML(html); })
    .catch(function () { if (btn) btn.disabled = false; });
}
function destroyStructure(id, playfield, name) {
  if (!confirm(I18N.confirmDestroy.replace('{name}', name))) return;
  structAction('/destroy', id, { playfield: playfield });
}

function factionStructure(id, name, currentFaction) {
  var fFaction = document.getElementById('f-faction-all');
  var options = fFaction ? Array.prototype.slice.call(fFaction.options).filter(function (o) { return o.value; }) : [];
  var selectHtml = '<select id="modal-faction-select">' + options.map(function (o) {
    var selected = o.value === currentFaction ? ' selected' : '';
    return '<option value="' + o.value + '"' + selected + '>' + o.textContent + '</option>';
  }).join('') + '</select>';
  document.getElementById('modal-box').classList.remove('wide', 'fullscreen', 'blocks-breakdown-modal');
  document.getElementById('modal-title').textContent = I18N.factionModalTitle.replace('{name}', name);
  document.getElementById('modal-body').innerHTML =
    '<div class="task-form"><label>' + I18N.factionModalLabel + selectHtml + '</label></div>';
  var confirmBtn = document.getElementById('modal-confirm-btn');
  confirmBtn.textContent = I18N.modalConfirm;
  confirmBtn.style.display = '';
  var modalActionsEl = document.querySelector('.modal-actions');
  if (modalActionsEl) modalActionsEl.style.display = '';
  confirmBtn.onclick = function () {
    var abbrev = document.getElementById('modal-faction-select').value;
    if (abbrev) structAction('/faction', id, { abbrev: abbrev });
    closeModal();
  };
  showModal();
}

function refuelStructure(id, playfield, name) {
  document.getElementById('modal-box').classList.remove('wide', 'fullscreen', 'blocks-breakdown-modal');
  document.getElementById('modal-title').textContent = I18N.refuelModalTitle.replace('{name}', name);
  document.getElementById('modal-body').innerHTML =
    '<div class="task-form">' +
    '<label>' + I18N.refuelModalFuel + '<input type="number" id="modal-refuel-fuel" min="0" max="100" placeholder="%"></label>' +
    '<label>' + I18N.refuelModalOxygen + '<input type="number" id="modal-refuel-oxygen" min="0" max="100" placeholder="%"></label>' +
    '<label>' + I18N.refuelModalPentaxid + '<input type="number" id="modal-refuel-pentaxid" min="0" max="100" placeholder="%"></label>' +
    '</div>';
  var confirmBtn = document.getElementById('modal-confirm-btn');
  confirmBtn.textContent = I18N.modalConfirm;
  confirmBtn.style.display = '';
  var modalActionsEl = document.querySelector('.modal-actions');
  if (modalActionsEl) modalActionsEl.style.display = '';
  confirmBtn.onclick = function () {
    var extra = { playfield: playfield };
    ['fuel', 'oxygen', 'pentaxid'].forEach(function (key) {
      var v = document.getElementById('modal-refuel-' + key).value;
      if (v !== '') extra[key + 'Percent'] = v;
    });
    structAction('/refuel', id, extra);
    closeModal();
  };
  showModal();
}

function moveStructure(id, playfield, name, x, y, z) {
  document.getElementById('modal-box').classList.remove('wide', 'fullscreen', 'blocks-breakdown-modal');
  document.getElementById('modal-title').textContent = I18N.moveModalTitle.replace('{name}', name);
  document.getElementById('modal-body').innerHTML =
    '<div class="task-form">' +
    '<label>' + I18N.moveModalMode + '<select id="move-target-mode">' +
      '<option value="coords">' + I18N.warpModeCoords + '</option>' +
      '<option value="player">' + I18N.warpModePlayer + '</option>' +
    '</select></label>' +
    '<label id="move-player-row" style="display:none">' + I18N.warpEntityLabelPlayer + '<select id="move-player-select"></select></label>' +
    '<label style="flex-basis:100%">X/Y/Z' +
      '<div style="display:flex;gap:4px">' +
        '<input type="number" id="move-x" step="any" value="' + x + '" class="dur-input warp-coord-input">' +
        '<input type="number" id="move-y" step="any" value="' + y + '" class="dur-input warp-coord-input">' +
        '<input type="number" id="move-z" step="any" value="' + z + '" class="dur-input warp-coord-input">' +
      '</div>' +
    '</label>' +
    '</div>';
  var confirmBtn = document.getElementById('modal-confirm-btn');
  confirmBtn.textContent = I18N.modalConfirm;
  confirmBtn.style.display = '';
  var modalActionsEl = document.querySelector('.modal-actions');
  if (modalActionsEl) modalActionsEl.style.display = '';

  var moveTargets = null;
  function setCoordsDisabled(disabled) {
    ['move-x', 'move-y', 'move-z'].forEach(function (id) { document.getElementById(id).disabled = disabled; });
  }
  function applyPlayerList() {
    var select = document.getElementById('move-player-select');
    var players = ((moveTargets && moveTargets.players) || []).filter(function (p) { return p.playfield === playfield; });
    select.innerHTML = players.map(function (p) {
      var label = p.name + (p.online ? '' : ' (' + I18N.warpOffline + ')');
      return '<option value="' + p.steamId + '">' + label + '</option>';
    }).join('');
    onPlayerChange();
  }
  function onPlayerChange() {
    if (!moveTargets) return;
    var steamId = document.getElementById('move-player-select').value;
    var match = (moveTargets.players || []).filter(function (p) { return p.steamId === steamId; })[0];
    if (match && match.pos) {
      document.getElementById('move-x').value = match.pos.x;
      document.getElementById('move-y').value = match.pos.y;
      document.getElementById('move-z').value = match.pos.z;
    }
  }
  function onModeChange() {
    var mode = document.getElementById('move-target-mode').value;
    document.getElementById('move-player-row').style.display = mode === 'player' ? '' : 'none';
    setCoordsDisabled(mode === 'player');
    if (mode === 'coords') {
      document.getElementById('move-x').value = x;
      document.getElementById('move-y').value = y;
      document.getElementById('move-z').value = z;
    } else if (moveTargets) {
      applyPlayerList();
    }
  }
  document.getElementById('move-target-mode').addEventListener('change', onModeChange);
  document.getElementById('move-player-select').addEventListener('change', onPlayerChange);
  fetch('/action/warp/targets').then(function (r) { return r.json(); }).then(function (data) {
    moveTargets = data;
    if (document.getElementById('move-target-mode').value === 'player') applyPlayerList();
  });

  confirmBtn.onclick = function () {
    structAction('/move', id, {
      playfield: playfield,
      x: document.getElementById('move-x').value,
      y: document.getElementById('move-y').value,
      z: document.getElementById('move-z').value,
    });
    closeModal();
  };
  showModal();
}

function restoreStructure(id, name, existsHint) {
  if (existsHint === undefined) existsHint = true;
  var modalBox = document.getElementById('modal-box');
  modalBox.classList.remove('wide', 'fullscreen', 'blocks-breakdown-modal');
  modalBox.style.width = '730px';
  document.getElementById('modal-title').textContent = I18N.restoreModalTitle.replace('{name}', name);
  var radioRow = 'style="flex-basis:100%;flex-direction:row;align-items:center;gap:4px"';
  document.getElementById('modal-body').innerHTML =
    '<div class="task-form">' +
    '<label style="flex:0 0 130px;min-width:0">' + I18N.restoreModalDate + '<br><select id="modal-restore-date" disabled style="width:100%;min-width:0"><option>' + I18N.restoreModalSnapshotLoading + '</option></select></label>' +
    '<label style="flex:0 0 550px;min-width:0">' + I18N.restoreModalSnapshot + '<br><select id="modal-restore-snapshot" disabled style="width:100%;min-width:0"><option>' + I18N.restoreModalSnapshotLoading + '</option></select></label>' +
    '<label ' + radioRow + '><input type="radio" name="modal-restore-mode" value="containers"' + (existsHint ? ' checked' : ' disabled') + '> ' + I18N.restoreModalContainers + '</label>' +
    '<label ' + radioRow + '><input type="radio" name="modal-restore-mode" value="structure"> ' + I18N.restoreModalStructure + '</label>' +
    '<label ' + radioRow + '><input type="radio" name="modal-restore-mode" value="all"' + (existsHint ? '' : ' checked') + '> ' + I18N.restoreModalAll + '</label>' +
    '<div id="modal-restore-warning" class="warning-banner" style="flex-basis:100%;display:none">' + I18N.restoreModalAllWarning + '</div>' +
    '<div id="modal-restore-position-group" style="flex-basis:100%;display:none">' +
    '<div style="font-size:12px;color:var(--text-dim);margin-bottom:2px">' + I18N.restoreModalPositionLabel + '</div>' +
    '<label ' + radioRow + '><input type="radio" name="modal-restore-position" value="snapshot" checked> ' + I18N.restoreModalPositionSnapshot + '</label>' +
    '<label ' + radioRow + '><input type="radio" name="modal-restore-position" value="player"> ' + I18N.restoreModalPositionPlayer + '</label>' +
    '<select id="modal-restore-position-player" disabled style="display:none"><option>' + I18N.restoreModalPositionPlayerLoading + '</option></select>' +
    '</div>' +
    '</div>';
  var warning = document.getElementById('modal-restore-warning');
  function updateWarning() {
    warning.style.display = document.querySelector('input[name="modal-restore-mode"]:checked').value !== 'containers' ? '' : 'none';
  }
  Array.prototype.forEach.call(document.getElementsByName('modal-restore-mode'), function (r) {
    r.addEventListener('change', updateWarning);
  });
  updateWarning();
  var positionPlayerSelect = document.getElementById('modal-restore-position-player');
  Array.prototype.forEach.call(document.getElementsByName('modal-restore-position'), function (r) {
    r.addEventListener('change', function () {
      var atPlayer = document.querySelector('input[name="modal-restore-position"]:checked').value === 'player';
      positionPlayerSelect.style.display = atPlayer ? '' : 'none';
    });
  });
  var confirmBtn = document.getElementById('modal-confirm-btn');
  confirmBtn.textContent = I18N.restoreModalConfirm;
  confirmBtn.style.display = '';
  var modalActionsEl = document.querySelector('.modal-actions');
  if (modalActionsEl) modalActionsEl.style.display = '';
  confirmBtn.onclick = function () {
    var mode = document.querySelector('input[name="modal-restore-mode"]:checked').value;
    var snapshotId = document.getElementById('modal-restore-snapshot').value || '0';
    var atPlayer = document.querySelector('input[name="modal-restore-position"]:checked').value === 'player';
    var positionPlayerId = atPlayer ? (positionPlayerSelect.value || '0') : '0';
    structAction('/restore', id, { mode: mode, snapshotId: snapshotId, positionPlayerId: positionPlayerId });
    closeModal();
  };
  showModal();

  fetch('/action/structures/' + id + '/backups')
    .then(function (r) { return r.json(); })
    .then(function (list) {
      var dateSelect = document.getElementById('modal-restore-date');
      var select = document.getElementById('modal-restore-snapshot');
      if (!select || !dateSelect) return; 
      if (!list || list.length === 0) {
        dateSelect.innerHTML = '<option value="">' + I18N.restoreModalSnapshotNone + '</option>';
        select.innerHTML = '<option value="0">' + I18N.restoreModalSnapshotNone + '</option>';
        return;
      }
      function snapshotSummary(s) {
        var parts = [I18N.restoreModalSnapshotSummary.replace('{containers}', s.containerCount).replace('{items}', s.itemCount)];
        if (s.blockCount) parts.push(I18N.restoreModalSnapshotBlocks.replace('{blocks}', s.blockCount));
        if (s.fuelPercent !== undefined) parts.push(I18N.restoreModalSnapshotFuel.replace('{percent}', Math.round(s.fuelPercent)));
        if (s.oxygenPercent !== undefined) parts.push(I18N.restoreModalSnapshotOxygen.replace('{percent}', Math.round(s.oxygenPercent)));
        if (s.pentaxidPercent !== undefined) parts.push(I18N.restoreModalSnapshotPentaxid.replace('{percent}', Math.round(s.pentaxidPercent)));
        return parts.join(', ');
      }
      var byDate = {};
      var dateOrder = [];
      list.forEach(function (s) {
        var date = s.timestamp.split(' ')[0];
        if (!byDate[date]) { byDate[date] = []; dateOrder.push(date); }
        byDate[date].push(s);
      });
      function renderSnapshotsForDate(date) {
        select.innerHTML = (byDate[date] || []).map(function (s) {
          var time = s.timestamp.split(' ')[1] || s.timestamp;
          return '<option value="' + s.id + '">' + time + ' — ' + snapshotSummary(s) + '</option>';
        }).join('');
        select.disabled = select.options.length === 0;
      }
      dateSelect.innerHTML = dateOrder.map(function (date) {
        return '<option value="' + date + '">' + date + ' (' + byDate[date].length + ')</option>';
      }).join('');
      dateSelect.disabled = false;
      dateSelect.onchange = function () { renderSnapshotsForDate(dateSelect.value); };
      renderSnapshotsForDate(dateOrder[0]);
      if (list[0].typeName !== 'BA') {
        var group = document.getElementById('modal-restore-position-group');
        if (group) group.style.display = '';
        fetch('/action/warp/targets')
          .then(function (r) { return r.json(); })
          .then(function (targets) {
            if (!positionPlayerSelect) return;
            var players = (targets && targets.players) || [];
            positionPlayerSelect.innerHTML = players.map(function (p) {
              var label = p.name + (p.online ? '' : ' (' + I18N.warpOffline + ')');
              return '<option value="' + p.entityId + '">' + label + '</option>';
            }).join('');
            positionPlayerSelect.disabled = false;
          });
      }
    })
    .catch(function () {
      var dateSelect = document.getElementById('modal-restore-date');
      if (dateSelect) dateSelect.innerHTML = '<option value="">' + I18N.restoreModalSnapshotNone + '</option>';
      var select = document.getElementById('modal-restore-snapshot');
      if (select) select.innerHTML = '<option value="0">' + I18N.restoreModalSnapshotNone + '</option>';
    });
}

function openBlocksBreakdownModal(id, name) {
  var modalBox = document.getElementById('modal-box');
  modalBox.classList.remove('fullscreen');
  modalBox.classList.add('wide', 'blocks-breakdown-modal');
  modalBox.style.width = '';
  document.getElementById('modal-title').textContent = I18N.blocksBreakdownTitle.replace('{name}', name);
  document.getElementById('modal-body').innerHTML =
    '<div class="dim" id="bd-timestamp" style="margin-bottom:8px"></div>' +
    '<div class="blocks-breakdown">' +
    '<div class="blocks-breakdown-group">' +
    '<div class="dim" style="margin-bottom:4px">' + I18N.blocksBreakdownBlocks + '</div>' +
    '<div id="bd-blocks" class="bd-list"></div>' +
    '</div>' +
    '<div class="blocks-breakdown-group">' +
    '<div class="dim" style="margin-bottom:4px">' + I18N.blocksBreakdownDevices + '</div>' +
    '<div id="bd-devices" class="bd-list"></div>' +
    '</div>' +
    '</div>';
  var confirmBtn = document.getElementById('modal-confirm-btn');
  confirmBtn.style.display = 'none';
  showModal();

  function renderList(container, items) {
    if (!items || items.length === 0) {
      container.innerHTML = '<div class="dim">' + I18N.blocksBreakdownEmpty + '</div>';
      return;
    }
    container.innerHTML = items.map(function (it) {
      return '<div class="bd-row">' +
        '<img class="bd-icon" loading="lazy" src="/icon/' + encodeURIComponent(it.key) + '" onerror="this.style.visibility=\'hidden\'">' +
        '<span class="bd-name">' + it.name + '</span>' +
        '<span class="bd-count">' + it.count + '</span>' +
        '</div>';
    }).join('');
  }

  fetch('/action/structures/' + id + '/blocks-breakdown')
    .then(function (r) { return r.json(); })
    .then(function (data) {
      var blocksEl = document.getElementById('bd-blocks');
      var devicesEl = document.getElementById('bd-devices');
      var tsEl = document.getElementById('bd-timestamp');
      if (!blocksEl || !devicesEl) return; 
      renderList(blocksEl, data.blocks);
      renderList(devicesEl, data.devices);
      if (tsEl) tsEl.textContent = data.timestamp ? I18N.blocksBreakdownAsOf.replace('{timestamp}', data.timestamp) : '';
    })
    .catch(function () {
      var blocksEl = document.getElementById('bd-blocks');
      var devicesEl = document.getElementById('bd-devices');
      if (blocksEl) blocksEl.innerHTML = '<div class="dim">' + I18N.requestError + '</div>';
      if (devicesEl) devicesEl.innerHTML = '';
    });
}

function structuresInitFilters(presets) {
  presets = presets || {};
  var rows = Array.prototype.slice.call(document.querySelectorAll('.structures-table tbody tr'));
  var search = document.getElementById('f-search');
  var fPlayfield = document.getElementById('f-playfield');
  var fFaction = document.getElementById('f-faction');
  var fOwner = document.getElementById('f-owner');
  var fType = document.getElementById('f-type');
  var fPoi = document.getElementById('f-poi');
  var fDeleted = document.getElementById('f-deleted');
  var fPageSize = document.getElementById('f-page-size');
  var counter = document.getElementById('structures-count');
  var pageInfo = document.getElementById('structures-page-info');
  var prevBtn = document.getElementById('structures-page-prev');
  var nextBtn = document.getElementById('structures-page-next');
  var page = 0;

  var quickLinkSystem = presets.system || '';

  function matchesFilters(row) {
    var q = search.value.trim().toLowerCase();
    var pf = fPlayfield.value, fac = fFaction.value, own = fOwner.value, typ = fType.value;
    var showPoi = fPoi.checked;
    var showDeleted = fDeleted.checked;
    if (q && row.dataset.name.toLowerCase().indexOf(q) === -1) return false;
    if (pf && row.dataset.playfield !== pf) return false;
    if (quickLinkSystem && row.dataset.system !== quickLinkSystem) return false;
    if (fac && row.dataset.faction !== fac) return false;
    if (own && row.dataset.owner !== own) return false;
    if (typ && row.dataset.type !== typ) return false;
    if (!showPoi && row.dataset.poi === '1') return false;
    if (!showDeleted && row.dataset.deleted === '1') return false;
    return true;
  }

  function applyFilters() {
    var matched = rows.filter(matchesFilters);
    var pageSize = parseInt(fPageSize.value, 10) || 50;
    var pages = matched.length > 0 ? Math.ceil(matched.length / pageSize) : 1;
    if (page >= pages) page = pages - 1;
    if (page < 0) page = 0;
    var start = page * pageSize, end = start + pageSize;
    var visible = new Set(matched.slice(start, end));

    rows.forEach(function (row) { row.style.display = visible.has(row) ? '' : 'none'; });

    counter.textContent = matched.length + ' / ' + rows.length;
    pageInfo.textContent = I18N.structuresPageInfo
      .replace('{page}', String(page + 1)).replace('{pages}', String(pages)).replace('{total}', String(matched.length));
    prevBtn.disabled = page <= 0;
    nextBtn.disabled = page >= pages - 1;
  }

  function onFilterChange() { page = 0; applyFilters(); }

  [search, fPlayfield, fFaction, fOwner, fType, fPoi, fDeleted, fPageSize].forEach(function (el) {
    el.addEventListener('input', onFilterChange);
    el.addEventListener('change', onFilterChange);
  });
  prevBtn.addEventListener('click', function () { page--; applyFilters(); });
  nextBtn.addEventListener('click', function () { page++; applyFilters(); });

  if (presets.owner) fOwner.value = presets.owner;
  if (presets.faction) fFaction.value = presets.faction;
  if (presets.playfield) fPlayfield.value = presets.playfield;

  applyFilters();
}

function openStructuresModal(filters) {
  document.getElementById('modal-title').textContent = I18N.structuresTitle;
  document.getElementById('modal-box').classList.add('fullscreen');
  fetch('/action/structures/fragment').then(function (r) { return r.text(); }).then(function (html) {
    document.getElementById('modal-body').innerHTML = html;
    var confirmBtn = document.getElementById('modal-confirm-btn');
    if (confirmBtn) confirmBtn.style.display = 'none';
    var modalActions = document.querySelector('.modal-actions');
    if (modalActions) modalActions.style.display = 'none';
    showModal();
    structuresInitFilters(filters || {});
  }).catch(function () {});
}
