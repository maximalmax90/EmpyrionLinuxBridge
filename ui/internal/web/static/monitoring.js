
(function () {
	var REFRESH_MS = 20000; 

	var charts = {}; 
	var currentRange = '24h';
	var customRange = null;
	var refreshTimer = null;
	var suppressZoomDepth = 0;
	function beginSuppressZoom() { suppressZoomDepth++; }
	function endSuppressZoomDeferred() {
		requestAnimationFrame(function () { suppressZoomDepth = Math.max(0, suppressZoomDepth - 1); });
	}

	var RANGE_SECONDS = { '1h': 3600, '6h': 21600, '24h': 86400, '7d': 604800 };

	var BASE_TICK_INCR = { '1h': 600, '6h': 3600, '24h': 4 * 3600, '7d': 86400 };

	var MIN_ZOOM_SECONDS = 60;

	var zoomIncrs = [
		10, 15, 30,
		60, 300, 600, 900, 1800,
		3600, 2 * 3600, 3 * 3600, 4 * 3600, 6 * 3600, 12 * 3600,
		86400, 2 * 86400, 7 * 86400, 14 * 86400, 30 * 86400,
		60 * 86400, 90 * 86400, 180 * 86400, 365 * 86400
	];

	function incrsForSpan(span) {
		for (var r in RANGE_SECONDS) {
			if (Math.abs(span - RANGE_SECONDS[r]) < 1) {
				var incr = BASE_TICK_INCR[r];
				return [incr, incr * 2, incr * 4];
			}
		}
		return zoomIncrs;
	}

	var dimColor = getComputedStyle(document.documentElement).getPropertyValue('--text-dim').trim() || '#5b6472';
	var gridColor = getComputedStyle(document.documentElement).getPropertyValue('--panel-border').trim() || '#c7ccd6';
	var accentColor = getComputedStyle(document.documentElement).getPropertyValue('--accent').trim() || '#9400d3';
	var PALETTE = [accentColor, '#e5635b', '#1a9c53', '#b5790a', '#3b82f6', '#c65cf0', '#0e9488', '#d13c3c'];
	function paletteColor(i) {
		if (i < PALETTE.length) return PALETTE[i];
		return 'hsl(' + Math.round((i * 137.508) % 360) + ', 65%, 45%)';
	}
	function naturalCompare(a, b) {
		var na = a.match(/\d+/), nb = b.match(/\d+/);
		if (na && nb) {
			var da = parseInt(na[0], 10), db = parseInt(nb[0], 10);
			if (da !== db) return da - db;
		}
		return a < b ? -1 : a > b ? 1 : 0;
	}
	var LEGEND_2COL_THRESHOLD = 6;

	function pad2(n) { return n < 10 ? '0' + n : '' + n; }

	function fmtAxisTime(u, splits, axisIdx, foundSpace, foundIncr) {
		return splits.map(function (ts) {
			var d = new Date(ts * 1000);
			if (foundIncr >= 30 * 86400) {
				return pad2(d.getDate()) + '.' + pad2(d.getMonth() + 1) + '.' + String(d.getFullYear()).slice(-2);
			}
			if (foundIncr >= 86400) {
				return pad2(d.getDate()) + '.' + pad2(d.getMonth() + 1);
			}
			if (foundIncr < 60) {
				return pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
			}
			return pad2(d.getHours()) + ':' + pad2(d.getMinutes());
		});
	}

	function fmtPercentAxis(u, vals) {
		return vals.map(function (v) { return v + '%'; });
	}

	function makeChart(el, labels, yValuesFmt, yRange, seriesValue) {
		var opts = {
			width: el.clientWidth || 360,
			height: 220,
			series: [{}].concat(labels.map(function (label, i) {
				var s = { label: label, stroke: paletteColor(i), width: 1.5, points: { show: false } };
				if (seriesValue) s.value = seriesValue;
				return s;
			})),
			axes: [
				{ stroke: dimColor, grid: { stroke: gridColor }, ticks: { stroke: gridColor }, values: fmtAxisTime, incrs: zoomIncrs, space: 40 },
				{ stroke: dimColor, grid: { stroke: gridColor }, ticks: { stroke: gridColor }, values: yValuesFmt || fmtPercentAxis }
			],
			scales: {
				x: {
					time: true,
					range: function (u, min, max) {
						var span = max - min;
						if (span < MIN_ZOOM_SECONDS) {
							var mid = (min + max) / 2;
							min = mid - MIN_ZOOM_SECONDS / 2;
							max = mid + MIN_ZOOM_SECONDS / 2;
						}
						return [min, max];
					}
				},
				y: yRange ? { range: yRange } : {}
			},
			cursor: { drag: { x: true, y: false } },
			legend: { show: labels.length > 1 },
			hooks: {
				setScale: [function (u, key) {
					if (key !== 'x') return;
					var list = incrsForSpan(u.scales.x.max - u.scales.x.min);
					u.axes[0].incrs = function () { return list; };
					if (suppressZoomDepth <= 0) {
						u.zoomed = true;
					}
				}]
			}
		};
		var initData = [[]];
		labels.forEach(function () { initData.push([]); });
		var chart = new uPlot(opts, initData, el);
		chart.zoomed = false;
		var legendEl = el.querySelector('.u-legend');
		if (legendEl && labels.length > LEGEND_2COL_THRESHOLD) {
			legendEl.classList.add('legend-2col');
			legendEl.style.gridTemplateRows = 'repeat(' + Math.ceil(labels.length / 2) + ', auto)';
		}
		return chart;
	}

	function currentSinceUntilSec() {
		if (customRange) {
			return [Math.floor(customRange.since.getTime() / 1000), Math.floor(customRange.until.getTime() / 1000)];
		}
		var now = Math.floor(Date.now() / 1000);
		return [now - RANGE_SECONDS[currentRange], now];
	}

	function forceRange(chart, resetZoom) {
		var min, max;
		if (chart.zoomed && !resetZoom) {
			min = chart.scales.x.min;
			max = chart.scales.x.max;
		} else {
			chart.zoomed = false;
			var su = currentSinceUntilSec();
			min = su[0];
			max = su[1];
		}
		chart.setScale('x', { min: min, max: max });
	}

	function ensureCharts() {
		if (charts.cpu) return;
		charts.cpu = makeChart(document.getElementById('chart-cpu'), ['CPU'], fmtPercentAxis, [0, 100]);
		charts.iowait = makeChart(document.getElementById('chart-iowait'), ['iowait'], fmtPercentAxis, [0, 100]);
		charts.mem = makeChart(document.getElementById('chart-mem'), ['RAM', 'Swap'], fmtPercentAxis, [0, 100], seriesValueFmt('%'));
	}

	function addCard(title) {
		var grid = document.getElementById('monitoring-grid');
		var card = document.createElement('div');
		card.className = 'monitoring-card';
		var h3 = document.createElement('h3');
		h3.textContent = title;
		var chartEl = document.createElement('div');
		card.appendChild(h3);
		card.appendChild(chartEl);
		grid.appendChild(card);
		return chartEl;
	}

	var chartLabels = {};
	function sameLabels(a, b) {
		if (a.length !== b.length) return false;
		for (var i = 0; i < a.length; i++) if (a[i] !== b[i]) return false;
		return true;
	}
	function discardChartIfLabelsChanged(key, labels) {
		if (!charts[key] || sameLabels(chartLabels[key] || [], labels)) return;
		var card = charts[key].root && charts[key].root.closest('.monitoring-card');
		charts[key].destroy();
		if (card && card.parentNode) card.parentNode.removeChild(card);
		delete charts[key];
	}

	function ensureDisksChart(labels) {
		discardChartIfLabelsChanged('disks', labels);
		if (charts.disks) return charts.disks;
		charts.disks = makeChart(addCard(I18N.filesystems), labels, fmtPercentAxis, [0, 100], seriesValueFmt('%'));
		chartLabels.disks = labels.slice();
		return charts.disks;
	}

	function fmtTempAxis(u, vals) {
		return vals.map(function (v) { return Math.round(v) + '°C'; });
	}
	function seriesValueFmt(unit) {
		return function (u, v) { return v == null ? '--' : Math.round(v) + unit; };
	}

	function fmtBps(v) {
		if (v == null) return '--';
		if (v >= 1024 * 1024) return (v / 1024 / 1024).toFixed(1) + ' MB/s';
		if (v >= 1024) return (v / 1024).toFixed(1) + ' KB/s';
		return Math.round(v) + ' B/s';
	}
	function fmtBpsCompact(v) {
		if (v == null) return '--';
		if (v >= 1024 * 1024) return (v / 1024 / 1024).toFixed(1) + 'M';
		if (v >= 1024) return Math.round(v / 1024) + 'K';
		return Math.round(v) + 'B';
	}
	function fmtBpsAxis(u, vals) {
		return vals.map(fmtBpsCompact);
	}
	function netSeriesValueFmt() {
		return function (u, v) { return fmtBps(v); };
	}

	var CPU_CHIPS = ['coretemp', 'k10temp', 'zenpower', 'cpu_thermal'];
	var GPU_CHIPS = ['nouveau', 'amdgpu', 'radeon', 'nvidia'];
	function startsWithAny(lower, prefixes) {
		for (var i = 0; i < prefixes.length; i++) {
			if (lower.indexOf(prefixes[i]) === 0) return true;
		}
		return false;
	}
	function classifyChip(chip) {
		var lower = chip.toLowerCase();
		if (startsWithAny(lower, CPU_CHIPS)) return 'CPU';
		if (lower.indexOf('nvme') === 0) return 'NVMe';
		if (startsWithAny(lower, GPU_CHIPS)) return 'GPU';
		return null;
	}
	function pickPackageLabels(series, preferSubstring) {
		var lower = preferSubstring.toLowerCase();
		var matched = series.filter(function (s) { return s.label.toLowerCase().indexOf(lower) !== -1; });
		return matched.length > 0 ? matched : [series[0]];
	}

	function ensureTempChart(labels) {
		discardChartIfLabelsChanged('temp', labels);
		if (charts.temp) return charts.temp;
		charts.temp = makeChart(addCard(I18N.temperatures), labels, fmtTempAxis, undefined, seriesValueFmt('°C'));
		chartLabels.temp = labels.slice();
		return charts.temp;
	}

	function ensureNetChart(labels) {
		discardChartIfLabelsChanged('net', labels);
		if (charts.net) return charts.net;
		charts.net = makeChart(addCard(I18N.network), labels, fmtBpsAxis, undefined, netSeriesValueFmt());
		chartLabels.net = labels.slice();
		return charts.net;
	}

	function fmtCountAxis(u, vals) {
		return vals.map(function (v) { return Math.round(v); });
	}
	function ensureActivityChart() {
		if (charts.activity) return charts.activity;
		charts.activity = makeChart(addCard(I18N.activity), [I18N.activityPlayfields, I18N.activityOnline], fmtCountAxis, undefined, seriesValueFmt(''));
		return charts.activity;
	}

	function setData(data, resetZoom) {
		var governor = (data && data.governor) || '';
		var cpuTitle = document.getElementById('chart-cpu-title');
		if (cpuTitle) cpuTitle.textContent = governor ? I18N.cpu + ' (' + governor + ')' : I18N.cpu;

		var empty = document.getElementById('monitoring-empty');
		var grid = document.getElementById('monitoring-grid');
		var samples = (data && data.samples) || [];
		if (samples.length === 0) {
			empty.hidden = false;
			empty.textContent = I18N.noData;
			grid.hidden = true;
			return;
		}
		empty.hidden = true;
		grid.hidden = false;
		ensureCharts();

		beginSuppressZoom();
		var ts = samples.map(function (s) { return Math.floor(new Date(s.ts).getTime() / 1000); });
		charts.cpu.setData([ts, samples.map(function (s) { return s.cpuPercent; })]);
		charts.iowait.setData([ts, samples.map(function (s) { return s.ioWaitPercent; })]);
		charts.mem.setData([ts, samples.map(function (s) { return s.memPercent; }), samples.map(function (s) { return s.swapPercent; })]);
		forceRange(charts.cpu, resetZoom);
		forceRange(charts.iowait, resetZoom);
		forceRange(charts.mem, resetZoom);

		var disks = (data && data.disks) || {};
		var diskKeys = Object.keys(disks).sort();
		if (diskKeys.length > 0) {
			var disksChart = ensureDisksChart(diskKeys);
			var dts = [];
			diskKeys.forEach(function (mount) {
				var pts = disks[mount] || [];
				if (pts.length > dts.length) dts = pts.map(function (p) { return Math.floor(new Date(p.ts).getTime() / 1000); });
			});
			var dframes = [dts];
			diskKeys.forEach(function (mount) {
				var byTs = {};
				(disks[mount] || []).forEach(function (p) { byTs[Math.floor(new Date(p.ts).getTime() / 1000)] = p.percent; });
				dframes.push(dts.map(function (t) { return byTs.hasOwnProperty(t) ? byTs[t] : null; }));
			});
			disksChart.setData(dframes);
			forceRange(disksChart, resetZoom);
		}

		var sensors = (data && data.sensors) || {};
		var byChip = {};
		Object.keys(sensors).forEach(function (key) {
			var slash = key.indexOf('/');
			var chip = slash === -1 ? key : key.slice(0, slash);
			var label = slash === -1 ? key : key.slice(slash + 1);
			(byChip[chip] || (byChip[chip] = [])).push({ label: label, points: sensors[key] || [] });
		});
		var rawByCategory = { CPU: [], GPU: [], NVMe: [] };
		Object.keys(byChip).sort().forEach(function (chip) {
			var category = classifyChip(chip);
			if (!category) return;
			var series = byChip[chip].sort(function (a, b) { return naturalCompare(a.label, b.label); });
			if (category === 'CPU') {
				pickPackageLabels(series, 'package').forEach(function (s) { rawByCategory.CPU.push(s); });
			} else if (category === 'NVMe') {
				pickPackageLabels(series, 'composite').forEach(function (s) { rawByCategory.NVMe.push(s); });
			} else {
				rawByCategory.GPU.push(series[0]);
			}
		});
		var tempSeries = [];
		['CPU', 'GPU', 'NVMe'].forEach(function (category) {
			var list = rawByCategory[category];
			list.forEach(function (s, i) {
				tempSeries.push({ label: list.length > 1 ? category + ' ' + (i + 1) : category, points: s.points });
			});
		});
		if (tempSeries.length > 0) {
			var tempChart = ensureTempChart(tempSeries.map(function (s) { return s.label; }));
			var tts = [];
			tempSeries.forEach(function (s) {
				if (s.points.length > tts.length) tts = s.points.map(function (p) { return Math.floor(new Date(p.ts).getTime() / 1000); });
			});
			var tframes = [tts];
			tempSeries.forEach(function (s) {
				var byTs = {};
				s.points.forEach(function (p) { byTs[Math.floor(new Date(p.ts).getTime() / 1000)] = p.c; });
				tframes.push(tts.map(function (t) { return byTs.hasOwnProperty(t) ? byTs[t] : null; }));
			});
			tempChart.setData(tframes);
			forceRange(tempChart, resetZoom);
		}

		var net = (data && data.net) || {};
		var netSeries = [];
		Object.keys(net).sort().forEach(function (iface) {
			var pts = net[iface] || [];
			netSeries.push({ label: iface + ' ' + I18N.netIn, points: pts, field: 'rxBps' });
			netSeries.push({ label: iface + ' ' + I18N.netOut, points: pts, field: 'txBps' });
		});
		if (netSeries.length > 0) {
			var netChart = ensureNetChart(netSeries.map(function (s) { return s.label; }));
			var nts = [];
			netSeries.forEach(function (s) {
				if (s.points.length > nts.length) nts = s.points.map(function (p) { return Math.floor(new Date(p.ts).getTime() / 1000); });
			});
			var nframes = [nts];
			netSeries.forEach(function (s) {
				var byTs = {};
				s.points.forEach(function (p) { byTs[Math.floor(new Date(p.ts).getTime() / 1000)] = p[s.field]; });
				nframes.push(nts.map(function (t) { return byTs.hasOwnProperty(t) ? byTs[t] : null; }));
			});
			netChart.setData(nframes);
			forceRange(netChart, resetZoom);
		}

		var activity = (data && data.activity) || [];
		if (activity.length > 0) {
			var activityChart = ensureActivityChart();
			var ats = activity.map(function (p) { return Math.floor(new Date(p.ts).getTime() / 1000); });
			activityChart.setData([
				ats,
				activity.map(function (p) { return p.playfieldsActive; }),
				activity.map(function (p) { return p.onlinePlayers; })
			]);
			forceRange(activityChart, resetZoom);
		}

		endSuppressZoomDeferred();
	}

	function load(resetZoom) {
		var url = customRange
			? '/monitoring/data?since=' + encodeURIComponent(customRange.since.toISOString()) + '&until=' + encodeURIComponent(customRange.until.toISOString())
			: '/monitoring/data?range=' + encodeURIComponent(currentRange);
		fetch(url)
			.then(function (r) {
				if (!r.ok) throw new Error(r.status);
				return r.json();
			})
			.then(function (data) { setData(data, resetZoom); })
			.catch(function () {
				var empty = document.getElementById('monitoring-empty');
				empty.hidden = false;
				empty.textContent = I18N.loadError;
				document.getElementById('monitoring-grid').hidden = true;
			});
	}

	document.querySelectorAll('.range-btn').forEach(function (btn) {
		btn.addEventListener('click', function () {
			document.querySelectorAll('.range-btn').forEach(function (b) { b.classList.remove('btn-active'); });
			btn.classList.add('btn-active');
			currentRange = btn.getAttribute('data-range');
			customRange = null;
			document.getElementById('monitoring-date-from').value = '';
			document.getElementById('monitoring-date-to').value = '';
			load(true);
		});
	});

	(function () {
		var fromEl = document.getElementById('monitoring-date-from');
		var toEl = document.getElementById('monitoring-date-to');
		var today = new Date().toISOString().slice(0, 10);
		fromEl.max = today;
		toEl.max = today;
		document.getElementById('monitoring-date-apply').addEventListener('click', function () {
			if (!fromEl.value || !toEl.value) return;
			var since = new Date(fromEl.value + 'T00:00:00');
			var until = new Date(toEl.value + 'T23:59:59.999');
			if (since >= until) return;
			customRange = { since: since, until: until };
			document.querySelectorAll('.range-btn').forEach(function (b) { b.classList.remove('btn-active'); });
			load(true);
		});
	})();

	load(true);
	refreshTimer = setInterval(load, REFRESH_MS);
	window.addEventListener('beforeunload', function () { clearInterval(refreshTimer); });
})();
