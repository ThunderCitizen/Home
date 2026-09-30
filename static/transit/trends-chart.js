// Route comparison chart and KPI card interaction.
(function () {
  'use strict';
  const tc = ThemeColors();

  const KPI_LABELS = {
    otp: 'On-Time Performance',
    cancel: 'Cancellation Rate',
    cv: 'Bus Spacing',
    notice: 'Cancel Notice',
    ewt: 'Excess Wait Time',
    wait: 'Worst-Stop Wait'
  };

  const MONTHS = ['', 'Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

  function readRouteColors() {
    return (window.RouteMeta && window.RouteMeta.colors) || {};
  }
  function readRouteMetaById() {
    return (window.RouteMeta && window.RouteMeta.byId) || {};
  }

  // Read active KPI from server-rendered data attribute, default to 'otp'.
  let reportKPI = (function() {
    const grid = document.querySelector('.kpi-grid[data-active-kpi]');
    return grid ? grid.getAttribute('data-active-kpi') : 'otp';
  })();

  // Cancel log data — embedded by the server via @templ.JSONScript.
  function loadCancelledTrips() {
    const el = document.getElementById('cancelled-trips');
    if (!el) return [];
    try { return JSON.parse(el.textContent || '[]') || []; }
    catch (_e) { return []; }
  }

  function init() {
    cancelLogData = loadCancelledTrips();
    initReportCardChart();
    initRouteCompareChart();
    initLongTermTrend();
    if (reportKPI === 'cancel') toggleCancelPanels(true);
  }

  // ==========================================================================
  // KPI CARD SELECTION
  // ==========================================================================

  function initReportCardChart() {
    const cards = document.querySelectorAll('.kpi-card[data-kpi]');
    for (let i = 0; i < cards.length; i++) {
      cards[i].addEventListener('keydown', function(e) { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); selectReportKPI(e.currentTarget.getAttribute('data-kpi')); } });
      cards[i].addEventListener('click', function (e) {
        const kpi = e.currentTarget.getAttribute('data-kpi');
        if (kpi) selectReportKPI(kpi);
      });
    }
  }

  function selectReportKPI(kpi) {
    reportKPI = kpi;

    const cards = document.querySelectorAll('.kpi-card[data-kpi]');
    for (let i = 0; i < cards.length; i++) {
      cards[i].setAttribute('aria-pressed', String(cards[i].getAttribute('data-kpi') === kpi));
      if (cards[i].getAttribute('data-kpi') === kpi) {
        cards[i].classList.add('kpi-active');
      } else {
        cards[i].classList.remove('kpi-active');
      }
    }

    if (window.updateRouteCompare) window.updateRouteCompare(kpi);
    if (window.updateMetricTrend) window.updateMetricTrend();
    toggleCancelPanels(kpi === 'cancel');
  }

  // ==========================================================================
  // ROUTE COMPARISON BAR CHART
  //
  // Reads embedded chunk data via window.transitChunks.loadChunks() and
  // aggregates per route in the browser. No fetch — the data is on the
  // page from first paint.
  // ==========================================================================

  function buildRouteCompareData() {
    if (!window.transitChunks) return [];
    const chunks = window.transitChunks.loadChunks();
    const routeMeta = readRouteMetaById();

    const grouped = window.transitChunks.groupByRoute(chunks);
    const out = [];
    grouped.forEach(function(routeChunks, routeID) {
      const meta = routeMeta[routeID] || { short_name: routeID };
      out.push({
        route_id: routeID,
        short_name: meta.short_name || routeID,
        otp:    window.transitChunks.kpi(routeChunks, 'otp',    ''),
        cancel: window.transitChunks.kpi(routeChunks, 'cancel', ''),
        ewt:    window.transitChunks.kpi(routeChunks, 'ewt',    ''),
        cv:     window.transitChunks.kpi(routeChunks, 'cv',     ''),
      });
    });
    return out;
  }

  function initRouteCompareChart() {
    const container = document.getElementById('route-compare-chart');
    if (!container) return;
    const routeColors = readRouteColors();

    const routes = buildRouteCompareData();
    if (!routes.length) return;

    const KPI_TO_COMPARE = { otp: 'otp', cancel: 'cancel', cv: 'cv', ewt: 'ewt', notice: 'cancel', wait: 'ewt' };
    let currentMetric = KPI_TO_COMPARE[reportKPI] || 'otp';

    const COMPARE_METRICS = {
      otp:    { key: 'otp',    label: 'On-Time %',         unit: '%',    lower: false, fmt: function(v) { return v.toFixed(1); } },
      cancel: { key: 'cancel', label: 'Cancellation Rate', unit: '%',    lower: true,  fmt: function(v) { return v.toFixed(1); } },
      cv:     { key: 'cv',     label: 'Bus Spacing', unit: '',    lower: true,  fmt: function(v) { return v.toFixed(2); } },
      ewt:    { key: 'ewt',    label: 'Excess Wait Time (min)',  unit: '',     lower: true,  fmt: function(v) { return v.toFixed(1); } },
    };

    const sorted = routes.slice().sort(function (a, b) {
      const an = parseInt(a.short_name, 10) || 999;
      const bn = parseInt(b.short_name, 10) || 999;
      return an - bn || a.short_name.localeCompare(b.short_name);
    });

    function buildDOM() {
      let html = '<div class="rc-label"></div>';
      html += '<div class="rc-cols">';
      for (let j = 0; j < sorted.length; j++) {
        const r = sorted[j];
        const color = routeColors[r.route_id] || routeColors[r.short_name] || tc.statusMuted;
        html += '<div class="rc-col">';
        html += '<span class="rc-val"></span>';
        html += '<div class="rc-col-track"><span class="rc-zero"></span><div class="rc-col-bar" style="height:0;background:' + color + ';color:' + color + '"></div></div>';
        html += '<span class="rc-badge" style="color:' + color + '">' + r.short_name + '</span>';
        html += '</div>';
      }
      html += '</div>';
      container.innerHTML = html;
    }

    function updateBars() {
      const m = COMPARE_METRICS[currentMetric];
      if (!m) return;

      let maxVal = 0, minVal = 0;
      for (let i = 0; i < sorted.length; i++) {
        if (sorted[i][m.key] > maxVal) maxVal = sorted[i][m.key];
        if (sorted[i][m.key] < minVal) minVal = sorted[i][m.key];
      }
      if (maxVal === minVal) maxVal = minVal + 1;
      const span = maxVal - minVal, zero = -minVal / span * 100;

      const labelEl = container.querySelector('.rc-label');
      if (labelEl) labelEl.textContent = m.label + (m.lower ? ' \u2014 lower is better' : ' \u2014 higher is better');

      const bars = container.querySelectorAll('.rc-col-bar');
      const vals = container.querySelectorAll('.rc-val');
      for (let k = 0; k < sorted.length; k++) {
        const val = sorted[k][m.key];
        const pct = val == null ? 0 : Math.abs(val) / span * 100;
        if (bars[k]) bars[k].style.bottom = (val < 0 ? (val-minVal)/span*100 : zero) + '%';
        const baseline = container.querySelectorAll('.rc-zero')[k];
        if (baseline) baseline.style.bottom = zero + '%';
        if (bars[k]) bars[k].style.height = pct.toFixed(0) + '%';
        if (vals[k]) vals[k].textContent = val == null ? '—' : m.fmt(val) + m.unit;
        if (bars[k]) bars[k].setAttribute('aria-label', val == null ? 'Insufficient observations' : m.fmt(val) + m.unit);
      }

      const titleEl = document.getElementById('route-compare-title');
      if (titleEl) titleEl.textContent = 'Route Comparison — ' + m.label;
    }

    // Build DOM, force layout, then animate to initial values
    buildDOM();
    requestAnimationFrame(function () { updateBars(); });

    window.updateRouteCompare = function (kpi) {
      const mapped = KPI_TO_COMPARE[kpi] || kpi;
      if (COMPARE_METRICS[mapped]) {
        currentMetric = mapped;
        updateBars();
      }
    };
  }

  // ==========================================================================
  // LONG-TERM TREND (follows the selected KPI)
  // ==========================================================================

  // D3 draws the series; aggregation lives in chunks.js and is tested against Go.
  function initLongTermTrend() {
    const container = document.getElementById('metrics-trend-chart');
    const el = document.getElementById('transit-trend-chunks');
    if (!container || !el || !window.transitChunks || !window.d3) return;
    let chunks;
    try { chunks = JSON.parse(el.textContent || '[]') || []; } catch (_e) { return; }
    const helpers = window.transitChunks;
    const routeSelect = document.getElementById('trend-route');
    const names = readRouteMetaById();
    Array.from(helpers.groupByRoute(chunks).keys()).sort(function(a,b) { return a.localeCompare(b, undefined, {numeric: true}); }).forEach(function(id) {
      const option = document.createElement('option'); option.value = id;
      option.textContent = 'Route ' + ((names[id] || {}).short_name || id); routeSelect.appendChild(option);
    });
    const dates = chunks.map(function(c) { return c.date; }).sort();
    function display(value) { return value == null ? 'No reading' : helpers.format(value, reportKPI) + (reportKPI === 'otp' || reportKPI === 'cancel' ? '%' : reportKPI === 'ewt' ? ' min' : ''); }
    function render() {
      container.replaceChildren();
      const rows = chunks.filter(function(c) { return !routeSelect.value || c.route_id === routeSelect.value; });
      const title = document.getElementById('metrics-trend-title'); title.textContent = 'Trend — ' + KPI_LABELS[reportKPI];
      if (!dates.length) { container.textContent = 'No trend data is available yet.'; return; }
      const series = helpers.trendSeries(rows, reportKPI, dates[0], container.dataset.to || dates[dates.length - 1], '');
      const readings = series.flatMap(function(d) { return [d.daily, d.rolling]; }).filter(function(v) { return v != null; });
      if (!readings.length) { container.textContent = 'Not enough data for this selection. Try another route.'; return; }
      const width = Math.max(280, container.clientWidth || 720), height = 280, margin = {left: 48, right: 24, top: 18, bottom: 32};
      // Include the entire final day, so month shading also works for a single reading.
      const x = d3.scaleUtc().domain([series[0].at, d3.utcDay.offset(series[series.length - 1].at, 1)]).range([margin.left, width-margin.right]);
      const extent = d3.extent(readings), pad = (extent[1] - extent[0] || 1) * .12;
      const y = d3.scaleLinear().domain([Math.min(0, extent[0]-pad), Math.max(1, extent[1]+pad)]).nice().range([height-margin.bottom, margin.top]);
      if (reportKPI === 'otp') y.domain([0, 100]);
      if (reportKPI === 'cancel') {
        // Keep zero visible while fitting the displayed rates, including spikes.
        y.domain([0, Math.max(1, extent[1] * 1.12)]).nice(5);
        y.domain([0, Math.min(100, y.domain()[1])]);
      }
      const svg = d3.select(container).append('svg').attr('class', 'metrics-trend-svg').attr('viewBox', [0,0,width,height])
        .attr('role', 'group').attr('tabindex', 0)
        .attr('aria-label', KPI_LABELS[reportKPI] + ': daily readings and 30-day average. ' + container.dataset.monthLabel + ' is shaded. Use the left and right arrow keys to explore dates.');
      const monthStart = new Date(container.dataset.month + '-01T00:00:00Z');
      if (Number.isFinite(+monthStart)) {
        const left = Math.max(margin.left, x(monthStart));
        const right = Math.min(width - margin.right, x(d3.utcMonth.offset(monthStart, 1)));
        if (right > left) {
          svg.append('rect').attr('class', 'mt-selected-month')
            .attr('x', left).attr('y', margin.top).attr('width', right - left).attr('height', height - margin.top - margin.bottom);
          svg.append('line').attr('class', 'mt-month-boundary')
            .attr('x1', left).attr('x2', left).attr('y1', margin.top).attr('y2', height - margin.bottom);
        }
      }
      svg.append('g').attr('class','mt-axis').attr('transform','translate(0,'+(height-margin.bottom)+')').call(d3.axisBottom(x).ticks(width < 500 ? 4 : 7).tickFormat(d3.utcFormat('%b %d')).tickSizeOuter(0));
      const yAxis = d3.axisLeft(y).ticks(4).tickSizeOuter(0);
      if (reportKPI === 'otp' || reportKPI === 'cancel') yAxis.tickFormat(function(value) { return value + '%'; });
      if (reportKPI === 'ewt') yAxis.tickFormat(function(value) { return value + 'm'; });
      svg.append('g').attr('class','mt-axis').attr('transform','translate('+margin.left+',0)').call(yAxis);
      const line = d3.line().defined(function(d) { return d.daily != null; }).x(function(d) { return x(d.at); }).y(function(d) { return y(d.daily); });
      svg.append('path').datum(series).attr('class','mt-line mt-daily').attr('d',line);
      svg.selectAll('.mt-dot').data(series.filter(function(d) { return d.daily != null; })).join('circle').attr('class','mt-dot').attr('cx',function(d){return x(d.at);}).attr('cy',function(d){return y(d.daily);}).attr('r',1.7);
      line.defined(function(d) { return d.rolling != null; }).y(function(d) { return y(d.rolling); });
      svg.append('path').datum(series).attr('class','mt-line mt-line-average').attr('d',line);
      const focus = svg.append('line').attr('class','mt-focus').attr('y1',margin.top).attr('y2',height-margin.bottom).attr('visibility','hidden');
      const detail = document.createElement('div');
      detail.className = 'metrics-trend-tooltip';
      detail.setAttribute('role', 'status');
      detail.hidden = true;
      container.appendChild(detail);
      let selectedIndex = series.length - 1;
      function inspect(index) {
        selectedIndex = Math.max(0, Math.min(series.length - 1, index));
        const d = series[selectedIndex];
        focus.attr('x1', x(d.at)).attr('x2', x(d.at)).attr('visibility', 'visible');
        detail.replaceChildren();
        [d3.utcFormat('%b %-d, %Y')(d.at), 'Daily: ' + display(d.daily), '30-day average: ' + display(d.rolling)].forEach(function(text, i) {
          const line = document.createElement(i === 0 ? 'strong' : 'span');
          line.textContent = text;
          detail.appendChild(line);
        });
        detail.hidden = false;
        detail.style.left = Math.max(0, Math.min(container.clientWidth - detail.offsetWidth, x(d.at) / width * container.clientWidth - detail.offsetWidth / 2)) + 'px';
      }
      function dismiss() {
        detail.hidden = true;
        focus.attr('visibility', 'hidden');
      }
      svg.on('pointermove pointerdown', function(event) {
        const at = x.invert(d3.pointer(event, this)[0]);
        inspect(d3.bisector(function(d) { return d.at; }).center(series, at));
      });
      svg.on('pointerleave', function() { if (document.activeElement !== this) dismiss(); });
      svg.on('focus', function() { inspect(selectedIndex); }).on('blur', dismiss);
      svg.on('keydown', function(event) {
        if (event.key === 'Escape') { dismiss(); return; }
        if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
        event.preventDefault();
        inspect(event.key === 'Home' ? 0 : event.key === 'End' ? series.length - 1 : selectedIndex + (event.key === 'ArrowLeft' ? -1 : 1));
      });
    }
    routeSelect.addEventListener('change', function() {
      render();
      if (reportKPI === 'cancel' && updateCancelLog) updateCancelLog();
    });
    window.updateMetricTrend=render;
    let observedWidth=0;
    new ResizeObserver(function(entries){const width=Math.round(entries[0].contentRect.width);if(width!==observedWidth){observedWidth=width;render();}}).observe(container);
    render();
  }

  let cancelLogData = null;
  let updateCancelLog = null;

  function toggleCancelPanels(show) {
    const el = document.getElementById('cancel-log-container');
    if (el) el.hidden = !show;
    if (show) {
      if (updateCancelLog) updateCancelLog();
      else renderCancelLog();
    }
  }

  function renderCancelLog() {
    const container = document.getElementById('cancel-log');
    if (!container || !cancelLogData) return;

    const routeSelect = document.getElementById('trend-route');
    const allChunks = window.transitChunks ? window.transitChunks.loadChunks() : [];
    const routeColors = readRouteColors();
    const routeNames = {};
    const metaEntries = (window.RouteMeta && window.RouteMeta.entries) || [];
    for (let i = 0; i < metaEntries.length; i++) {
      routeNames[metaEntries[i].route_id] = metaEntries[i].short_name;
    }

    const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

    // Build flat row data with sortable values
    const rows = [];
    for (let r = 0; r < cancelLogData.length; r++) {
      const tr = cancelLogData[r];
      const name = routeNames[tr.route_id] || tr.route_id;
      const color = routeColors[tr.route_id] || routeColors[name] || tc.statusMuted;
      const timeRange = tr.start_time + (tr.end_time ? ' – ' + tr.end_time : '');
      const reported = tr.first_seen || '';
      let notice = '';
      let noticeBad = true;
      let noticeSortVal = -9999;
      if (Number.isFinite(tr.lead_min)) {
        // The server keeps the service date; subtracting HH:MM strings loses
        // overnight and previous-day notice.
        const diff = tr.lead_min;
        noticeSortVal = diff;
        const absDiff = Math.abs(diff);
        const dur = absDiff >= 60 ? Math.floor(absDiff / 60) + 'h ' + (absDiff % 60) + 'm' : absDiff + 'm';
        if (diff >= 119) {
          // TB Transit's GTFS-RT TripUpdate feed only publishes cancellations
          // ~2h before departure, so anything at the ceiling is "at least 2h"
          // — the real notice given to riders may be much longer.
          notice = '2h+ before';
          noticeBad = false;
        } else if (diff > 0) {
          notice = dur + ' before';
          noticeBad = diff < 15;
        } else {
          notice = dur + ' after';
        }
      }
      const dt = new Date(tr.date + 'T12:00:00');
      const dayLabel = DAYS[dt.getDay()] + ', ' + MONTHS[dt.getMonth() + 1] + ' ' + dt.getDate();
      rows.push({
        routeID: tr.route_id, date: tr.date, dayLabel: dayLabel, name: name, color: color,
        timeRange: timeRange, startTime: tr.start_time, reported: reported,
        reportedMin: reported ? parseInt(reported.split(':')[0], 10) * 60 + parseInt(reported.split(':')[1], 10) : 0,
        notice: notice, noticeBad: noticeBad, noticeSortVal: noticeSortVal,
        headsign: tr.headsign || '', routeNum: parseInt(name, 10) || 999
      });
    }

    const SORT_COLS = {
      date:     function (a, b) { return a.date.localeCompare(b.date) || a.startTime.localeCompare(b.startTime); },
      route:    function (a, b) { return (a.routeNum - b.routeNum) || a.name.localeCompare(b.name); },
      time:     function (a, b) { return a.startTime.localeCompare(b.startTime); },
      headsign: function (a, b) { return a.headsign.localeCompare(b.headsign); },
      reported: function (a, b) { return a.reportedMin - b.reportedMin; },
      notice:   function (a, b) { return a.noticeSortVal - b.noticeSortVal; }
    };
    let currentSort = 'date';
    let sortAsc = false;

    function renderTable() {
      const selectedRoute = routeSelect ? routeSelect.value : '';
      const sorted = rows.filter(function(row) { return !selectedRoute || row.routeID === selectedRoute; });
      const cmp = SORT_COLS[currentSort] || SORT_COLS.date;
      sorted.sort(function (a, b) { return sortAsc ? cmp(a, b) : cmp(b, a); });

      const routeLabel = selectedRoute ? 'Route ' + (routeNames[selectedRoute] || selectedRoute) : '';
      let summary;
      if (!sorted.length) {
        summary = 'No reported cancellations' + (routeLabel ? ' for ' + routeLabel : '') + ' in the selected month.';
      } else {
        let totalScheduled = 0;
        for (let s = 0; s < allChunks.length; s++) {
          if (!selectedRoute || allChunks[s].route_id === selectedRoute) totalScheduled += allChunks[s].scheduled;
        }
        summary = (routeLabel ? routeLabel + ' · ' : '') + sorted.length + ' cancelled';
        if (totalScheduled > 0) {
          const pct = (sorted.length * 100 / totalScheduled).toFixed(1);
          summary += ' of ' + totalScheduled.toLocaleString() + ' total trips (' + pct + '%)';
        } else {
          summary += ' trips';
        }
        const days = new Set(sorted.map(function(row) { return row.date; })).size;
        summary += ' across ' + days + ' day' + (days !== 1 ? 's' : '');
      }
      container.querySelector('.cl-summary').textContent = summary;

      const groupByDate = currentSort === 'date';
      let thead = '<tr>';
      thead += '<th data-sort="date" class="cl-th-date">Date</th>';
      thead += '<th data-sort="route" class="cl-th-route">Route</th>';
      thead += '<th data-sort="time" class="cl-th-time">Scheduled</th>';
      thead += '<th data-sort="headsign" class="cl-th-headsign">Headsign</th>';
      thead += '<th data-sort="reported" class="cl-th-reported">Observed At</th>';
      thead += '<th data-sort="notice" class="cl-th-notice">Notice</th>';
      thead += '</tr>';

      let tbody = '';
      let lastDate = '';
      for (let r = 0; r < sorted.length; r++) {
        const row = sorted[r];
        const stripe = r % 2 === 1 ? ' cl-stripe' : '';
        let dateCell = '';
        let dateCls = '';
        if (groupByDate) {
          if (row.date !== lastDate) {
            dateCell = row.dayLabel;
            dateCls = ' cl-td-date-first';
            lastDate = row.date;
          }
        } else {
          dateCell = row.dayLabel;
        }
        tbody += '<tr class="' + stripe + '">';
        tbody += '<td class="cl-td-date' + dateCls + '">' + dateCell + '</td>';
        tbody += '<td class="cl-td-route" style="color:' + row.color + '">' + row.name + '</td>';
        tbody += '<td class="cl-td-time">' + row.timeRange + '</td>';
        tbody += '<td class="cl-td-headsign">' + row.headsign + '</td>';
        tbody += '<td class="cl-td-reported">' + row.reported + '</td>';
        tbody += '<td class="cl-td-notice ' + (row.noticeBad ? 'cl-notice-bad' : 'cl-notice-ok') + '">' + row.notice + '</td>';
        tbody += '</tr>';
      }

      const table = container.querySelector('.cl-table');
      if (table) {
        table.hidden = !sorted.length;
        table.querySelector('thead').innerHTML = thead;
        table.querySelector('tbody').innerHTML = tbody;
        const ths = table.querySelectorAll('th[data-sort]');
        for (let h = 0; h < ths.length; h++) {
          const key = ths[h].getAttribute('data-sort');
          ths[h].classList.toggle('cl-sort-active', key === currentSort);
          ths[h].classList.toggle('cl-sort-desc', key === currentSort && !sortAsc);
        }
        wireSorts(table);
      }

      // Mirror card list (mobile). CSS swaps table↔cards at md breakpoint.
      const cardList = container.querySelector('.cl-card-list');
      if (cardList) {
        cardList.hidden = !sorted.length;
        let cards = '';
        let lastCardDate = '';
        for (let cr = 0; cr < sorted.length; cr++) {
          const crow = sorted[cr];
          if (groupByDate && crow.date !== lastCardDate) {
            cards += '<div class="cl-card-divider">' + crow.dayLabel + '</div>';
            lastCardDate = crow.date;
          }
          const noticeCls = crow.notice ? (crow.noticeBad ? ' cl-card-bad' : ' cl-card-ok') : '';
          cards += '<article class="cl-card' + noticeCls + '">';
          cards += '<header class="cl-card-head">';
          cards += '<span class="cl-card-route" style="background:' + crow.color + '">' + crow.name + '</span>';
          cards += '<div class="cl-card-title">';
          if (crow.headsign) {
            cards += '<span class="cl-card-headsign">' + crow.headsign + '</span>';
          }
          cards += '</div>';
          cards += '</header>';
          cards += '<dl class="cl-card-fields">';
          cards += '<div class="cl-field"><dd>' + crow.timeRange + '</dd><dt>Scheduled</dt></div>';
          cards += '<span class="cl-field-sep"></span>';
          cards += '<div class="cl-field"><dd>' + (crow.reported || '<span class="cl-card-empty">—</span>') + '</dd><dt>Observed</dt></div>';
          if (crow.notice) {
            cards += '<span class="cl-field-sep"></span>';
            cards += '<div class="cl-field ' + (crow.noticeBad ? 'cl-field-bad' : 'cl-field-ok') + '"><dd>' + crow.notice + '</dd><dt>Notice</dt></div>';
          }
          if (!groupByDate) {
            cards += '<span class="cl-field-sep"></span>';
            cards += '<div class="cl-field"><dd>' + crow.dayLabel + '</dd><dt>Date</dt></div>';
          }
          cards += '</dl>';
          cards += '</article>';
        }
        cardList.innerHTML = cards;
      }
    }

    function wireSorts(table) {
      const ths = table.querySelectorAll('th[data-sort]');
      for (let h = 0; h < ths.length; h++) {
        ths[h].addEventListener('click', function (e) {
          const key = e.currentTarget.getAttribute('data-sort');
          if (currentSort === key) {
            sortAsc = !sortAsc;
          } else {
            currentSort = key;
            sortAsc = true;
          }
          renderTable();
        });
      }
    }

    let html = '<div class="cl-summary" role="status"></div>';
    html += '<table class="cl-table"><thead></thead><tbody></tbody></table>';
    html += '<div class="cl-card-list" role="list"></div>';
    container.innerHTML = html;

    updateCancelLog = renderTable;
    renderTable();
  }

  // ==========================================================================
  // HELPERS
  // ==========================================================================

  // HTML duration: compact decimal hours — "20.4<small>h</small>", "45<small>m</small>"
  window.fmtDurHTML = fmtDurHTML;
  function fmtDurHTML(minutes) {
    const u = '<small class="kpi-unit">';
    const ue = '</small>';
    const h = minutes / 60;
    if (h >= 1) {
      const s = h >= 10 ? Math.round(h).toString() : h.toFixed(1).replace(/\.0$/, '');
      return s + u + 'h' + ue;
    }
    return Math.round(minutes) + u + 'm' + ue;
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
