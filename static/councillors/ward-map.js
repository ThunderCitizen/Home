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

  const infoBar = document.getElementById("ward-info-bar");
  let geojsonLayer = null;
  let activeWard = null;

  // Ward → councillor name lookup, embedded by the template.
  const WARD_COUNCILLORS = (function () {
    const el = document.getElementById("ward-councillors");
    if (!el) return {};
    try { return JSON.parse(el.textContent || "{}"); } catch (e) { return {}; }
  })();

  function showInfo(name) {
    if (!infoBar || !name) return;
    const color = WARD_COLORS[name] || tc.statusMuted;
    const councillor = WARD_COUNCILLORS[name];
    const line = councillor
      ? '<span class="ward-name">' + name + ' Ward</span>' +
        '<span class="ward-councillor">' + councillor + '</span>'
      : '<span class="ward-name">' + name + ' Ward</span>';
    infoBar.innerHTML =
      '<span class="ward-dot" style="background:' + color + '"></span>' + line;
    infoBar.classList.add("info-bar-visible");
  }

  function hideInfo() {
    if (!infoBar) return;
    infoBar.classList.remove("info-bar-visible");
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
