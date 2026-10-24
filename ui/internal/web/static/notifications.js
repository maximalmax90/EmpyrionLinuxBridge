(function () {
  var lastUnreadCount = null; 

  function badgeEl() { return document.getElementById('notif-badge'); }

  function pulseBadge() {
    var el = badgeEl();
    if (!el) return;
    el.classList.remove('pulse');
    void el.offsetWidth;
    el.classList.add('pulse');
    setTimeout(function () { el.classList.remove('pulse'); }, 1800);
  }

  function setBadge(n) {
    var el = badgeEl();
    if (!el) return;
    if (n > 0) {
      el.textContent = n;
      el.classList.remove('hidden');
    } else {
      el.textContent = '';
      el.classList.add('hidden');
    }
    if (n > 0 && (lastUnreadCount === null || n > lastUnreadCount)) pulseBadge();
    lastUnreadCount = n;
  }

  function refreshUnreadCount() {
    fetch('/action/notifications/unread-count')
      .then(function (r) { return r.json(); })
      .then(function (v) { setBadge(v.count || 0); })
      .catch(function () {});
  }

  function swapDropdown(html) {
    var inner = document.getElementById('notif-dropdown-inner');
    if (inner) inner.innerHTML = html;
    refreshUnreadCount();
  }

  function loadDropdown() {
    fetch('/action/notifications')
      .then(function (r) { return r.text(); })
      .then(swapDropdown)
      .catch(function () {});
  }

  function positionDropdown(dd) {
    dd.style.left = '0';
    var margin = 8;
    var rect = dd.getBoundingClientRect();
    var overflowRight = rect.right - (window.innerWidth - margin);
    if (overflowRight > 0) {
      dd.style.left = (-overflowRight) + 'px';
    }
    var rect2 = dd.getBoundingClientRect();
    if (rect2.left < margin) {
      dd.style.left = (parseFloat(dd.style.left) + (margin - rect2.left)) + 'px';
    }
  }

  window.toggleNotifDropdown = function () {
    var dd = document.getElementById('notif-dropdown');
    if (!dd) return;
    var opening = dd.style.display === 'none' || !dd.style.display;
    if (!opening) {
      dd.style.display = 'none';
      return;
    }
    dd.style.display = 'block';
    loadDropdown();
    requestAnimationFrame(function () { positionDropdown(dd); });
  };

  window.markNotificationRead = function (id) {
    fetch('/action/notifications/' + id + '/read', { method: 'POST' })
      .then(function (r) { return r.text(); })
      .then(swapDropdown)
      .catch(function () {});
  };

  window.markAllNotificationsRead = function () {
    fetch('/action/notifications/read-all', { method: 'POST' })
      .then(function (r) { return r.text(); })
      .then(swapDropdown)
      .catch(function () {});
  };

  window.clearAllNotifications = function () {
    fetch('/action/notifications/clear', { method: 'POST' })
      .then(function (r) { return r.text(); })
      .then(swapDropdown)
      .catch(function () {});
  };

  document.addEventListener('click', function (e) {
    var menu = document.querySelector('.notif-menu');
    var dd = document.getElementById('notif-dropdown');
    if (!menu || !dd || dd.style.display === 'none') return;
    if (!menu.contains(e.target)) dd.style.display = 'none';
  });

  document.addEventListener('keydown', function (e) {
    if (e.key !== 'Escape') return;
    var restartOverlay = document.getElementById('restart-confirm-overlay');
    if (restartOverlay && restartOverlay.style.display !== 'none') {
      closeRestartConfirmModal();
      return;
    }
    var overlay = document.getElementById('modal-overlay');
    if (overlay && overlay.style.display !== 'none' && typeof window.closeModal === 'function') {
      window.closeModal();
    }
  });

  refreshUnreadCount();
  setInterval(refreshUnreadCount, 20000);

  var toastHideTimer = null;
  window.showToastHTML = function (html) {
    document.getElementById('toast').innerHTML = html;
    clearTimeout(toastHideTimer);
    toastHideTimer = setTimeout(function () { document.getElementById('toast').innerHTML = ''; }, 4000);
  };

  var restartConfirmBtn = null;

  function closeRestartConfirmModal() {
    var el = document.getElementById('restart-confirm-overlay');
    if (el) el.style.display = 'none';
  }

  function ensureRestartConfirmModal() {
    var el = document.getElementById('restart-confirm-overlay');
    if (el) return el;
    el = document.createElement('div');
    el.id = 'restart-confirm-overlay';
    el.className = 'modal-overlay';
    el.style.display = 'none';
    el.innerHTML =
      '<div class="modal-box">' +
        '<h3 id="restart-confirm-title"></h3>' +
        '<p id="restart-confirm-body"></p>' +
        '<div class="modal-actions">' +
          '<button type="button" class="btn-small" id="restart-confirm-cancel"></button>' +
          '<button type="button" class="btn-small btn-danger" id="restart-confirm-ok"></button>' +
        '</div>' +
      '</div>';
    el.addEventListener('click', function (e) { if (e.target === el) closeRestartConfirmModal(); });
    document.body.appendChild(el);
    document.getElementById('restart-confirm-cancel').onclick = closeRestartConfirmModal;
    document.getElementById('restart-confirm-ok').onclick = function () {
      closeRestartConfirmModal();
      if (!restartConfirmBtn) return;
      var action = restartConfirmBtn.dataset.confirmAction;
      restartConfirmBtn.disabled = true;
      htmx.ajax('POST', action, { target: '#topbar-status', swap: 'innerHTML' });
    };
    return el;
  }

  window.openRestartConfirm = function (btn) {
    restartConfirmBtn = btn;
    var el = ensureRestartConfirmModal();
    document.getElementById('restart-confirm-title').textContent = btn.dataset.confirmTitle || '';
    document.getElementById('restart-confirm-body').textContent = btn.dataset.confirmBody || '';
    document.getElementById('restart-confirm-cancel').textContent = btn.dataset.cancelLabel || '';
    document.getElementById('restart-confirm-ok').textContent = btn.dataset.confirmLabel || '';
    el.style.display = 'flex';
  };
})();
