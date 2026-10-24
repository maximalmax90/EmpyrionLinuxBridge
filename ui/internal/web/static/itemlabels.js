
var _itemRenderCfg = { localizedNames: true, showDescriptions: true, renderTags: true, darkBackground: false };
function initItemRenderConfig(cfg) {
  if (cfg) _itemRenderCfg = cfg;
}

function applyItemDarkBg(containerEl) {
  if (containerEl) containerEl.classList.toggle('item-dark-bg', !!_itemRenderCfg.darkBackground);
}

function bbcodeToHtml(raw) {
  if (!raw) return '';
  var s = String(raw)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
  s = s.replace(/\\n/g, '<br>');
  if (_itemRenderCfg.renderTags) {
    s = s
      .replace(/\[c\]\[([0-9a-fA-F]{3,8})\]/g, '<span style="color:#$1">')
      .replace(/\[-\]/g, '')
      .replace(/\[\/c\]/g, '</span>')
      .replace(/\[b\]/g, '<b>').replace(/\[\/b\]/g, '</b>')
      .replace(/\[i\]/g, '<i>').replace(/\[\/i\]/g, '</i>')
      .replace(/\[u\]/g, '<u>').replace(/\[\/u\]/g, '</u>')
      .replace(/\[sup\]/g, '<sup>').replace(/\[\/sup\]/g, '</sup>');
  }
  s = s.replace(/\[[^\[\]]{1,20}\]/g, '');
  return s;
}

function itemDisplayHtml(displayNames, name) {
  if (!_itemRenderCfg.localizedNames) return name;
  var real = displayNames && displayNames[name];
  return real ? bbcodeToHtml(real) : name;
}

function bbcodeToPlainText(raw) {
  if (!raw) return '';
  return String(raw).replace(/\\n/g, ' ').replace(/\[[^\[\]]{1,20}\]/g, '');
}

function itemMatchesSearch(displayNames, name, query, id) {
  var q = (query || '').trim().toLowerCase();
  if (!q) return true;
  if (name.toLowerCase().indexOf(q) !== -1) return true;
  if (_itemRenderCfg.localizedNames) {
    var real = displayNames && displayNames[name];
    if (real && bbcodeToPlainText(real).toLowerCase().indexOf(q) !== -1) return true;
  }
  if (id != null && String(id).indexOf(q) !== -1) return true;
  return false;
}

var _itemTooltipEl = null;
function initItemTooltip() {
  if (_itemTooltipEl) return _itemTooltipEl;
  var el = document.createElement('div');
  el.className = 'item-desc-tooltip';
  el.style.display = 'none';
  document.body.appendChild(el);
  _itemTooltipEl = el;
  return el;
}

function showItemTooltip(anchorEl, html) {
  var el = initItemTooltip();
  applyItemDarkBg(el);
  el.innerHTML = html;
  el.style.display = 'block';
  var rect = anchorEl.getBoundingClientRect();
  var top = rect.bottom + 4;
  var left = rect.left;
  var maxLeft = window.innerWidth - el.offsetWidth - 8;
  if (left > maxLeft) left = Math.max(8, maxLeft);
  if (top + el.offsetHeight > window.innerHeight) top = rect.top - el.offsetHeight - 4;
  el.style.top = Math.max(4, top) + 'px';
  el.style.left = left + 'px';
}

function hideItemTooltip() {
  if (_itemTooltipEl) _itemTooltipEl.style.display = 'none';
}

function positionItemPickerDropdown(anchorEl, dropdownEl) {
  dropdownEl.classList.remove('item-picker-open-up');
  var maxHeight = 320; 
  var neededHeight = Math.min(dropdownEl.scrollHeight || maxHeight, maxHeight);
  var rect = anchorEl.getBoundingClientRect();
  var spaceBelow = window.innerHeight - rect.bottom;
  var spaceAbove = rect.top;
  if (spaceBelow < neededHeight && spaceAbove > spaceBelow) {
    dropdownEl.classList.add('item-picker-open-up');
  }
}

function attachItemTooltip(rowEl, descriptions, name) {
  if (!_itemRenderCfg.showDescriptions) return;
  var desc = descriptions && descriptions[name];
  if (!desc) return;
  var html = bbcodeToHtml(desc);
  rowEl.addEventListener('mouseenter', function () { showItemTooltip(rowEl, html); });
  rowEl.addEventListener('mouseleave', hideItemTooltip);
}
