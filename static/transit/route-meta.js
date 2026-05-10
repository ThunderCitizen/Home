// Thunder Citizen — shared route metadata loader.
//
// Parses the server-rendered <script id="route-meta"> JSON once and
// exposes lookup tables on window.RouteMeta. Every transit page that
// embeds route-meta should include this script before any consumer
// (terminal-board, trends-chart, transit-map, transit-report).
(function () {
  "use strict";

  var entries = [];
  var byId = {};
  var colors = {};
  var texts = {};
  var names = {};
  var terminals = {};

  try {
    var el = document.getElementById("route-meta");
    if (el) {
      var arr = JSON.parse(el.textContent || "[]") || [];
      for (var i = 0; i < arr.length; i++) {
        var m = arr[i];
        if (!m || !m.route_id) continue;
        entries.push(m);
        byId[m.route_id] = m;
        if (m.color) {
          colors[m.route_id] = m.color;
          if (m.short_name) colors[m.short_name] = m.color;
        }
        if (m.text_color) texts[m.route_id] = m.text_color;
        if (m.name) names[m.route_id] = m.name;
        if (m.terminals && m.terminals.length) terminals[m.route_id] = m.terminals;
      }
    }
  } catch (_e) { /* malformed JSON shouldn't break the page */ }

  window.RouteMeta = {
    entries: entries,
    byId: byId,
    colors: colors,
    texts: texts,
    names: names,
    terminals: terminals,
  };
})();
