(function () {
  "use strict";

  const tc = ThemeColors();

  const WARD_COLORS = {
    "Current River": "#60a5fa",
    "McIntyre":      "#4ade80",
    "McKellar":      "#fbbf24",
    "Westfort":      "#f87171",
    "Neebing":       "#c084fc",
    "Northwood":     "#22d3ee",
    "Red River":     "#f472b6",
  };

  const el = document.getElementById("ward-map");
  const map = L.map("ward-map");
  el._leafletMap = map;

  var tiles = ThunderMapTiles();
  tiles.addTo(map);

  const overlay = document.getElementById("ward-info-overlay");
  const infoBody = document.getElementById("ward-info-body");
  const closeBtn = document.getElementById("ward-info-close");
  let geojsonLayer = null;
  let activeWard = null;

  // Ward → councillor name lookup, embedded by the template.
  const WARD_COUNCILLORS = (function () {
    const el = document.getElementById("ward-councillors");
    if (!el) return {};
    try { return JSON.parse(el.textContent || "{}"); } catch (e) { return {}; }
  })();

  function renderInfo(name) {
    if (!infoBody || !name) return;
    const color = WARD_COLORS[name] || tc.statusMuted;
    const councillor = WARD_COUNCILLORS[name];
    let html =
      '<div class="ward-info-head">' +
        '<span class="ward-dot" style="background:' + color + '"></span>' +
        '<span class="ward-name">' + name + ' Ward</span>' +
      '</div>';
    if (councillor) {
      html +=
        '<dl class="ward-info-meta">' +
          '<dt>Councillor</dt>' +
          '<dd class="ward-councillor">' + councillor + '</dd>' +
        '</dl>';
    }
    infoBody.innerHTML = html;
  }

  function showInfo(name) {
    if (!overlay || !name) return;
    renderInfo(name);
    overlay.classList.add("is-open");
  }

  function hideInfo() {
    if (!overlay) return;
    overlay.classList.remove("is-open");
  }

  if (closeBtn) {
    closeBtn.addEventListener("click", function () {
      if (activeWard && geojsonLayer) {
        geojsonLayer.eachLayer(function (l) { geojsonLayer.resetStyle(l); });
        activeWard = null;
      }
      hideInfo();
    });
  }

  // Layer bar toggle
  const layerBar = document.querySelector("[data-layer='wards']");
  let wardsVisible = layerBar ? layerBar.classList.contains("active") : true;

  if (layerBar) {
    layerBar.addEventListener("click", function () {
      wardsVisible = !wardsVisible;
      layerBar.classList.toggle("active", wardsVisible);
      if (geojsonLayer) {
        if (wardsVisible) {
          geojsonLayer.addTo(map);
        } else {
          map.removeLayer(geojsonLayer);
          activeWard = null;
          hideInfo();
        }
      }
    });
  }

  fetch("/static/councillors/thunder-bay-wards.geojson")
    .then(function (r) { return r.json(); })
    .then(function (data) {
      geojsonLayer = L.geoJSON(data, {
        style: function (feature) {
          return {
            color: WARD_COLORS[feature.properties.name] || tc.statusMuted,
            weight: 2,
            fillOpacity: 0.25,
          };
        },
        onEachFeature: function (feature, layer) {
          const name = feature.properties.name;

          layer.bindTooltip(name, {
            permanent: true,
            direction: "center",
            className: "ward-label",
          });

          layer.on("mouseover", function () {
            if (activeWard === name) return;
            layer.setStyle({ fillOpacity: 0.45, weight: 3 });
            showInfo(name);
          });

          layer.on("mouseout", function () {
            if (activeWard === name) return;
            geojsonLayer.resetStyle(layer);
            hideInfo();
          });

          layer.on("click", function () {
            if (activeWard === name) {
              activeWard = null;
              geojsonLayer.resetStyle(layer);
              hideInfo();
              return;
            }
            // Reset previous
            if (activeWard) {
              geojsonLayer.eachLayer(function (l) { geojsonLayer.resetStyle(l); });
            }
            activeWard = name;
            layer.setStyle({ fillOpacity: 0.45, weight: 3 });
            showInfo(name);
          });
        },
      }).addTo(map);

      map.fitBounds(geojsonLayer.getBounds(), { padding: [10, 10] });

      map.on("click", function (e) {
        if (!e.originalEvent._wardHandled && activeWard) {
          activeWard = null;
          geojsonLayer.eachLayer(function (l) { geojsonLayer.resetStyle(l); });
          hideInfo();
        }
      });
    });
})();
