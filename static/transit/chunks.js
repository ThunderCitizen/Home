// Shared reducers for versioned transit chunks. Mirrored and tested against chunk.KPI.
(function() {
  'use strict';

  // ---------------------------------------------------------------------
  // Loader — read the embedded JSON once on first call.
  // ---------------------------------------------------------------------

  let _cached = null;

  function loadChunks() {
    if (_cached !== null) return _cached;
    const el = document.getElementById('transit-chunks');
    if (!el) {
      _cached = [];
      return _cached;
    }
    try {
      const parsed = JSON.parse(el.textContent || '[]');
      _cached = Array.isArray(parsed) ? parsed : [];
    } catch (_e) {
      _cached = [];
    }
    return _cached;
  }

  // ---------------------------------------------------------------------
  // Filters — pure, return new arrays.
  // ---------------------------------------------------------------------

  function filterByBand(chunks, band) {
    return chunks.filter(function(b) { return b.band === band; });
  }

  function filterByRoute(chunks, routeID) {
    return chunks.filter(function(b) { return b.route_id === routeID; });
  }

  function filterByDate(chunks, date) {
    return chunks.filter(function(b) { return b.date === date; });
  }

  // ---------------------------------------------------------------------
  // Grouping — return Map<string, ChunkView[]>.
  // ---------------------------------------------------------------------

  function groupBy(chunks, keyFn) {
    const out = new Map();
    for (let i = 0; i < chunks.length; i++) {
      const k = keyFn(chunks[i]);
      let arr = out.get(k);
      if (!arr) {
        arr = [];
        out.set(k, arr);
      }
      arr.push(chunks[i]);
    }
    return out;
  }

  function groupByRoute(chunks) { return groupBy(chunks, function(b) { return b.route_id; }); }
  function groupByDate(chunks)  { return groupBy(chunks, function(b) { return b.date; }); }
  function groupByBand(chunks)  { return groupBy(chunks, function(b) { return b.band; }); }

  function kpi(chunks, metric, band) {
    let numerator = 0, denominator = 0;
    const perRoute = new Map();
    chunks.forEach(function(c) {
      if (c.metric_version !== 1 || (band && c.band !== band)) return;
      if (metric === 'otp') { numerator += c.otp_on_time * 100; denominator += c.otp_count; }
      if (metric === 'cancel' && (c.trips > 0 || c.cancelled > 0)) { numerator += c.cancelled * 100; denominator += c.scheduled; }
      if (metric === 'notice') { numerator += c.no_notice * 100; denominator += c.cancelled; }
      if (metric === 'wait') { numerator += c.wait_observed_area / 60; denominator += c.window_seconds; }
      if (metric === 'ewt' || metric === 'cv') {
        const a = perRoute.get(c.route_id) || { numerator: 0, denominator: 0 };
        a.numerator += metric === 'ewt' ? (c.wait_observed_area - c.wait_scheduled_area) / 60 : c.cv_weighted_sum;
        a.denominator += metric === 'ewt' ? c.window_seconds : c.cv_weight;
        perRoute.set(c.route_id, a);
      }
    });
    perRoute.forEach(function(a) {
      if (a.denominator > 0) { numerator += a.numerator / a.denominator; denominator++; }
    });
    return denominator > 0 ? numerator / denominator : null;
  }

  function sampleInfo(chunks, metric) {
    let expected = 0, observed = 0, samples = 0, eligible = 0, windows = 0;
    const routes = new Set(), days = new Set();
    chunks.forEach(function(c) {
      if (c.metric_version !== 1) return;
      expected += c.expected_timepoints; observed += c.observed_timepoints;
      eligible += c.eligible_windows; windows += c.total_windows;
      samples += metric === 'otp' ? c.otp_count : metric === 'cancel' ? c.scheduled : c.eligible_windows;
      if (kpi([c], metric, '') != null) { routes.add(c.route_id); days.add(c.date); }
    });
    return { expected: expected, observed: observed, eligible: eligible, windows: windows,
      samples: samples, routes: routes.size, days: days.size };
  }

  // Calendar days, including absent days. A rolling value needs a full 30-day
  // window, at least 70% of the selected day type observed, and a reading today.
  // This is a display completeness rule, not a statistical confidence interval.
  function trendSeries(chunks, metric, from, to, dayType) {
    const byDate = groupByDate(chunks), out = [], dayMS = 86400000;
    const start = Date.parse(from + 'T00:00:00Z'), end = Date.parse(to + 'T00:00:00Z');
    function included(at) {
      const day = new Date(at).getUTCDay();
      return !dayType || (dayType === 'weekday' && day > 0 && day < 6) || (dayType === 'saturday' && day === 6) || (dayType === 'sunday' && day === 0);
    }
    for (let at = start; at <= end; at += dayMS) {
      const date = new Date(at).toISOString().slice(0, 10), rows = byDate.get(date) || [];
      const daily = included(at) ? kpi(rows, metric, '') : null;
      let windowRows = [], valid = 0, expectedDays = 0;
      for (let t = at - 29 * dayMS; t <= at; t += dayMS) {
        if (!included(t)) continue;
        expectedDays++;
        const slice = byDate.get(new Date(t).toISOString().slice(0, 10)) || [];
        if (kpi(slice, metric, '') != null) valid++;
        windowRows = windowRows.concat(slice);
      }
      const rolling = at - start >= 29 * dayMS && daily != null && valid >= Math.ceil(expectedDays * .7) ? kpi(windowRows, metric, '') : null;
      out.push({ date: date, at: new Date(at), daily: daily, rolling: rolling, days: valid,
        expectedDays: expectedDays, sample: sampleInfo(rows, metric) });
    }
    return out;
  }

  // ---------------------------------------------------------------------
  // Formatters — match the Go view_helpers.go output exactly.
  // ---------------------------------------------------------------------

  function format(value, metric) {
    if (value == null) return '\u2014';
    switch (metric) {
      case 'otp':    return value.toFixed(0);
      case 'cancel': return value.toFixed(1);
      case 'notice': return value.toFixed(0);
      case 'wait':   return value.toFixed(1);
      case 'ewt':    return value.toFixed(1);
      case 'cv':     return value.toFixed(2);
    }
    return String(value);
  }

  // ---------------------------------------------------------------------
  // Public surface
  // ---------------------------------------------------------------------

  window.transitChunks = {
    loadChunks: loadChunks,
    filterByBand: filterByBand,
    filterByRoute: filterByRoute,
    filterByDate: filterByDate,
    groupByRoute: groupByRoute,
    groupByDate: groupByDate,
    groupByBand: groupByBand,
    kpi: kpi,
    sampleInfo: sampleInfo,
    trendSeries: trendSeries,
    format: format
  };
})();
