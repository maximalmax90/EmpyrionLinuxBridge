
var chistPage = 0, chistTotal = 0;
var chistPendingFilters = null;
var chistSenderClasses = {};

var CHIST_CHANNELS = ['global', 'faction', 'alliance', 'server', 'singleplayer', 'banner'];

function chatHistoryChannelLabel(ch) {
  switch (ch) {
    case 'global': return I18N.chatChannelGlobal;
    case 'faction': return I18N.chatChannelFaction;
    case 'alliance': return I18N.chatChannelAlliance;
    case 'server': return I18N.chatChannelServer;
    case 'singleplayer': return I18N.chatChannelSingleplayer;
    case 'banner': return I18N.chatChannelBanner;
    default: return ch;
  }
}

function openChatHistoryModal(filters) {
  chistPendingFilters = filters || null;
  document.getElementById('modal-title').textContent = I18N.chatHistoryTitle;
  document.getElementById('modal-box').classList.add('fullscreen');
  document.getElementById('modal-body').innerHTML = chatHistoryFiltersHtml();

  var confirmBtn = document.getElementById('modal-confirm-btn');
  if (confirmBtn) confirmBtn.style.display = 'none';
  var modalActions = document.querySelector('.modal-actions');
  if (modalActions) modalActions.style.display = 'none';
  showModal();

  chatHistorySetup();
}

function initChatHistoryPage(containerId) {
  document.getElementById(containerId).innerHTML = chatHistoryFiltersHtml();
  chatHistorySetup();
}

function chatHistorySetup() {
  chistPage = 0; chistTotal = 0;

  fetch('/action/chat/sender-classes').then(function (r) { return r.json(); }).then(function (data) {
    chistSenderClasses = data || {};
  }).catch(function () { chistSenderClasses = {}; });

  fetch('/action/history/names').then(function (r) { return r.json(); }).then(function (data) {
    var steamIdByName = data.steamIdByName || {};
    var playerDatalist = document.getElementById('chist-player-datalist');
    Object.keys(steamIdByName).sort().forEach(function (name) {
      var opt = document.createElement('option');
      opt.value = name;
      playerDatalist.appendChild(opt);
    });
    var targetDatalist = document.getElementById('chist-target-datalist');
    historyUniqueSorted(Object.values(data.factionNames || {})).forEach(function (tag) {
      var opt = document.createElement('option');
      opt.value = tag;
      targetDatalist.appendChild(opt);
    });
    if (chistPendingFilters) {
      if (chistPendingFilters.sender) document.getElementById('chist-player-value').value = chistPendingFilters.sender;
      if (chistPendingFilters.channel) document.getElementById('chist-channel').value = chistPendingFilters.channel;
      if (chistPendingFilters.target) document.getElementById('chist-target').value = chistPendingFilters.target;
    }
    runChatHistoryQuery();
  }).catch(function () { runChatHistoryQuery(); });
}

function chatHistoryFiltersHtml() {
  var now = new Date();
  var weekAgo = new Date(now.getTime() - 7 * 24 * 3600 * 1000);
  var channelOptions = '<option value="">' + I18N.chatHistoryChannelAll + '</option>' +
    CHIST_CHANNELS.map(function (ch) { return '<option value="' + ch + '">' + chatHistoryChannelLabel(ch) + '</option>'; }).join('');

  return '<div class="hist-filter-row">' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupRange + '</legend><div class="field-grid">' +
      '<label>' + I18N.historyFrom + '<input type="datetime-local" id="chist-from" value="' + historyToLocalInput(weekAgo) + '"></label>' +
      '<label>' + I18N.historyTo + '<input type="datetime-local" id="chist-to" value="' + historyToLocalInput(now) + '"></label>' +
    '</div></fieldset>' +
    '<fieldset class="player-group"><legend>' + I18N.chatHistoryGroupParticipants + '</legend><div class="field-grid">' +
      '<label>' + I18N.chatHistoryPlayerLabel +
        '<input type="text" id="chist-player-value" list="chist-player-datalist" placeholder="' + I18N.chatHistoryPlayerAll + '">' +
      '</label>' +
      '<datalist id="chist-player-datalist"></datalist>' +
      '<label>' + I18N.chatHistoryChannelLabel + '<select id="chist-channel"><option value="">' + I18N.chatHistoryChannelAll + '</option>' +
        CHIST_CHANNELS.map(function (ch) { return '<option value="' + ch + '">' + chatHistoryChannelLabel(ch) + '</option>'; }).join('') +
      '</select></label>' +
      '<label>' + I18N.chatHistoryTargetLabel +
        '<input type="text" id="chist-target" list="chist-target-datalist" placeholder="' + I18N.chatHistoryTargetAll + '">' +
      '</label>' +
      '<datalist id="chist-target-datalist"></datalist>' +
    '</div></fieldset>' +
    '<fieldset class="player-group"><legend>' + I18N.historyGroupExtra + '</legend><div class="field-grid">' +
      '<label>' + I18N.historyLimitLabel + '<input type="number" id="chist-limit" value="200" min="1"></label>' +
    '</div></fieldset>' +
    '</div>' +
    '<div class="hist-run-row">' +
      '<button type="button" class="btn-small btn-primary" onclick="runChatHistoryQuery()">' + I18N.historyShow + '</button>' +
    '</div>' +
    '<div class="table-scroll"><table class="chat-table"><thead><tr>' +
      '<th class="col-compact">' + I18N.chatColTime + '</th><th class="col-compact">' + I18N.chatColChannel + '</th>' +
      '<th class="col-compact">' + I18N.chatColPlayer + '</th><th>' + I18N.chatColMessage + '</th>' +
    '</tr></thead><tbody id="chist-tbody"></tbody></table></div>' +
    '<div class="hist-actions">' +
      '<span id="chist-page-info" class="dim"></span>' +
      '<button type="button" class="btn-small nfi nfi-chevron-left" id="chist-page-prev" onclick="chatHistoryChangePage(-1)"></button>' +
      '<button type="button" class="btn-small nfi nfi-chevron-right" id="chist-page-next" onclick="chatHistoryChangePage(1)"></button>' +
    '</div>';
}

function runChatHistoryQuery() {
  chistPage = 0;
  chatHistoryFetchPage();
}

function chatHistoryChangePage(delta) {
  var next = chistPage + delta;
  if (next < 0) return;
  if (delta > 0 && next * chatHistoryCurrentLimit() >= chistTotal) return;
  chistPage = next;
  chatHistoryFetchPage();
}

function chatHistoryCurrentLimit() {
  return parseInt(document.getElementById('chist-limit').value, 10) || 200;
}

function chatHistoryFetchPage() {
  var fromEl = document.getElementById('chist-from'), toEl = document.getElementById('chist-to');
  if (!fromEl || !toEl) return;
  var limit = chatHistoryCurrentLimit();
  var playerValue = document.getElementById('chist-player-value').value.trim();
  var body = {
    from: new Date(fromEl.value).toISOString(),
    to: new Date(toEl.value).toISOString(),
    sender: playerValue,
    channel: document.getElementById('chist-channel').value,
    target: document.getElementById('chist-target').value.trim(),
    limit: limit,
    offset: chistPage * limit,
  };
  var tbody = document.getElementById('chist-tbody');
  tbody.innerHTML = '<tr><td colspan="4" class="dim">' + I18N.historyLoading + '</td></tr>';
  fetch('/action/chat/history', {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
  }).then(function (r) { return r.json(); }).then(function (data) {
    var entries = (data && data.entries) || [];
    chistTotal = (data && data.total) || 0;
    chistPendingFilters = null;
    chatHistoryRenderPager(limit);
    renderChatHistoryResults(entries);
  }).catch(function () {
    tbody.innerHTML = '<tr><td colspan="4" class="dim">' + I18N.requestError + '</td></tr>';
  });
}

function chatHistoryRenderPager(limit) {
  var pages = chistTotal > 0 ? Math.ceil(chistTotal / limit) : 1;
  var info = document.getElementById('chist-page-info');
  var prevBtn = document.getElementById('chist-page-prev');
  var nextBtn = document.getElementById('chist-page-next');
  if (!info || !prevBtn || !nextBtn) return;
  info.textContent = I18N.historyPageInfo
    .replace('{page}', String(chistPage + 1))
    .replace('{pages}', String(pages))
    .replace('{total}', String(chistTotal));
  prevBtn.disabled = chistPage <= 0;
  nextBtn.disabled = (chistPage + 1) * limit >= chistTotal;
}

function chatBannerPriorityLabel(p) {
  switch (p) {
    case 'alarm': return I18N.bannerPriorityAlarm;
    case 'message': return I18N.bannerPriorityMessage;
    case 'info': return I18N.bannerPriorityInfo;
    default: return p;
  }
}

function renderChatHistoryResults(entries) {
  var tbody = document.getElementById('chist-tbody');
  if (!entries.length) {
    tbody.innerHTML = '<tr><td colspan="4" class="empty">' + I18N.chatEmpty + '</td></tr>';
    return;
  }
  tbody.innerHTML = entries.map(function (e) {
    var isBanner = e.channel === 'banner';
    var msgText = historyEscapeHtml(e.text);
    if (e.channel === 'singleplayer' && e.target) {
      msgText += ' <span class="chat-target">(&#8594; ' + historyEscapeHtml(e.target) + ')</span>';
    }
    var rowClass = 'chat-row channel-' + e.channel;
    var rowStyle = '';
    if (isBanner) {
      rowClass += ' priority-' + e.priority;
    } else {
      var sc = e.steamId ? (chistSenderClasses[e.steamId] || '') : 'server';
      rowStyle = sc ? ' style="background: color-mix(in srgb, var(--sender-' + sc + ', var(--ch-' + e.channel + ')) ' + (e.channel === 'server' ? 24 : 22) + '%, transparent)"' : '';
    }
    var middleCells;
    if (isBanner) {
      middleCells = '<td class="col-compact chat-channel" colspan="2">' + historyEscapeHtml(chatBannerPriorityLabel(e.priority)) + '</td>';
    } else {
      var channelText = e.channel === 'faction' && e.target ? '[' + historyEscapeHtml(e.target) + ']' : chatHistoryChannelLabel(e.channel);
      var playerText = historyEscapeHtml(e.sender);
      middleCells = '<td class="col-compact chat-channel">' + channelText + '</td><td class="col-compact">' + playerText + '</td>';
    }
    return '<tr class="' + rowClass + '"' + rowStyle + '>' +
      '<td class="col-compact chat-time">' + historyEscapeHtml(new Date(e.timestamp).toLocaleString()) + '</td>' +
      middleCells +
      '<td class="col-message">' + msgText + '</td>' +
    '</tr>';
  }).join('');
}
