function currentLogTab() {
  var active = document.querySelector('.log-tab.active');
  return active ? active.dataset.tab : 'daemon';
}

function activeSeverities() {
  var active = {};
  document.querySelectorAll('.log-sev-filter').forEach(function (cb) {
    if (cb.checked) active[cb.value] = true;
  });
  return active;
}

function applyLogFilters() {
  var tab = currentLogTab();
  var cat = null; 
  if (tab === 'mod') {
    cat = { mod: true };
  } else if (tab === 'server') {
    cat = {};
    document.querySelectorAll('.log-filter').forEach(function (cb) {
      if (cb.checked) cat[cb.value] = true;
    });
  }
  var sevFilters = document.querySelector('.log-filters-severity');
  if (sevFilters) sevFilters.style.display = (tab === 'server') ? 'none' : '';
  var sev = (tab === 'server') ? null : activeSeverities();
  document.querySelectorAll('.log-view tr.log-row').forEach(function (row) {
    var okCat = cat === null || cat[row.dataset.cat];
    var okSev = sev === null || sev[row.dataset.sev || ''];
    row.style.display = (okCat && okSev) ? '' : 'none';
  });
}

function setLogTab(tab) {
  document.querySelectorAll('.log-tab').forEach(function (b) {
    b.classList.toggle('active', b.dataset.tab === tab);
  });
  document.getElementById('log-view-daemon').style.display = (tab === 'daemon') ? '' : 'none';
  document.getElementById('log-view-webui').style.display = (tab === 'webui') ? '' : 'none';
  document.getElementById('log-view-server').style.display = (tab === 'mod' || tab === 'server') ? '' : 'none';
  document.getElementById('log-view-scheduler').style.display = (tab === 'scheduler') ? '' : 'none';
  var auditView = document.getElementById('log-view-audit');
  if (auditView) auditView.style.display = (tab === 'audit') ? '' : 'none';
  var filters = document.querySelector('.log-filters');
  if (filters) filters.style.display = (tab === 'server') ? '' : 'none';
  applyLogFilters();
  if (typeof refreshActiveLog === 'function') refreshActiveLog();
}

function restoreLogFilterState() {
  var saved;
  try { saved = JSON.parse(localStorage.getItem('elb_log_filters') || 'null'); }
  catch (e) { saved = null; }
  if (!saved) return;
  document.querySelectorAll('.log-filter, .log-sev-filter').forEach(function (cb) {
    var key = cb.classList.contains('log-sev-filter') ? 'sev:' + cb.value : 'cat:' + cb.value;
    if (Object.prototype.hasOwnProperty.call(saved, key)) cb.checked = saved[key];
  });
}
function saveLogFilterState() {
  var state = {};
  document.querySelectorAll('.log-filter, .log-sev-filter').forEach(function (cb) {
    var key = cb.classList.contains('log-sev-filter') ? 'sev:' + cb.value : 'cat:' + cb.value;
    state[key] = cb.checked;
  });
  localStorage.setItem('elb_log_filters', JSON.stringify(state));
}
document.addEventListener('DOMContentLoaded', function () {
  restoreLogFilterState();
  document.querySelectorAll('.log-filter, .log-sev-filter').forEach(function (cb) {
    cb.addEventListener('change', function () {
      saveLogFilterState();
      applyLogFilters();
    });
  });
  applyLogFilters();
});

var logRefreshTimer = null;
function logEndpointFor(tab) {
  if (tab === 'daemon') return { url: '/action/logs/daemon', target: 'daemon-log-inner' };
  if (tab === 'webui') return { url: '/action/logs/webui', target: 'webui-log-inner' };
  if (tab === 'scheduler') return { url: '/action/logs/scheduler', target: 'scheduler-log-inner' };
  if (tab === 'audit') return { url: '/action/logs/audit', target: 'audit-log-inner' };
  return { url: '/action/logs/server', target: 'server-log-inner' };
}
function refreshActiveLog() {
  var ep = logEndpointFor(currentLogTab());
  fetch(ep.url, { credentials: 'same-origin' })
    .then(function (r) { return r.text(); })
    .then(function (html) {
      var el = document.getElementById(ep.target);
      if (el) el.innerHTML = html;
      applyLogFilters();
    })
    .catch(function () {});
}
function scheduleLogRefresh() {
  if (logRefreshTimer) { clearInterval(logRefreshTimer); logRefreshTimer = null; }
  var select = document.getElementById('log-refresh-interval');
  if (!select) return;
  var ms = parseInt(select.value, 10) || 0;
  if (ms > 0) logRefreshTimer = setInterval(refreshActiveLog, ms);
}
document.addEventListener('DOMContentLoaded', function () {
  var select = document.getElementById('log-refresh-interval');
  if (!select) return; 
  var saved = localStorage.getItem('elb_log_refresh_ms');
  if (saved !== null) select.value = saved;
  select.addEventListener('change', function () {
    localStorage.setItem('elb_log_refresh_ms', select.value);
    scheduleLogRefresh();
  });
  var btn = document.getElementById('log-refresh-btn');
  if (btn) btn.addEventListener('click', refreshActiveLog);
  scheduleLogRefresh();
});
