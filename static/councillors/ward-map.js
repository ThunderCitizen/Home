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

  const WARD_COUNCILLORS = (function () {
    const node = document.getElementById("ward-councillors");
    if (!node) return {};
    try { return JSON.parse(node.textContent || "{}"); } catch (e) { return {}; }
  })();

  // tmi-layer info bar — same pattern as transit live map. Higher-priority
  // layer (selection) z-orders above hover via CSS; each layer toggles its
  // own .is-active / .is-clearing, no host-level state.
  const infoHost = document.querySelector(".ward-panel > .terminal-map-header");
  const layerClearTimers = { hover: 0, selection: 0 };
  let hoverHideTimer = 0;

  function getLayer(key) {
    return infoHost ? infoHost.querySelector(".tmi-" + key) : null;
  }
  function setLayer(key, html) {
    const layer = getLayer(key);
    if (!layer) return;
    if (layerClearTimers[key]) { clearTimeout(layerClearTimers[key]); layerClearTimers[key] = 0; }
    layer.classList.remove("is-clearing");
    layer.innerHTML = html;
    layer.classList.add("is-active");
  }
  function clearLayer(key) {
    const layer = getLayer(key);
    if (!layer) return;
    if (!layer.classList.contains("is-active")) { layer.innerHTML = ""; return; }
    layer.classList.remove("is-active");
    layer.classList.add("is-clearing");
    if (layerClearTimers[key]) clearTimeout(layerClearTimers[key]);
    layerClearTimers[key] = window.setTimeout(function () {
      layerClearTimers[key] = 0;
      const l = getLayer(key);
      if (l) { l.classList.remove("is-clearing"); l.innerHTML = ""; }
    }, 300);
  }
  function showHover(html) {
    if (hoverHideTimer) { clearTimeout(hoverHideTimer); hoverHideTimer = 0; }
    setLayer("hover", html);
  }
  function hideHoverDebounced() {
    if (hoverHideTimer) clearTimeout(hoverHideTimer);
    hoverHideTimer = window.setTimeout(function () {
      hoverHideTimer = 0;
      clearLayer("hover");
    }, 200);
  }

  function wardInfoHtml(name) {
    const color = WARD_COLORS[name] || tc.statusMuted;
    const councillor = WARD_COUNCILLORS[name];
    let html =
      '<span class="info-route" style="background:' + color + '">' + name + '</span>' +
      '<span class="info-name">' + name + ' Ward</span>';
    if (councillor) {
      html +=
        '<span class="info-sep" aria-hidden="true">—</span>' +
        '<span class="info-detail">' + councillor + '</span>';
    }
    return html;
  }

  // Suppress mouseover info on touch — taps fire it alongside the click.
  let lastPointerType = "mouse";
  document.addEventListener("pointerdown", function (e) {
    lastPointerType = e.pointerType || "mouse";
  }, true);

  let geojsonLayer = null;
  let activeWard = null;

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
          clearLayer("hover");
          clearLayer("selection");
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
            fillOpacity: 0.4,
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
            if (lastPointerType === "touch") return;
            if (activeWard !== name) {
              layer.setStyle({ weight: 4, opacity: 1 });
              layer.bringToFront();
            }
            showHover(wardInfoHtml(name));
          });

          layer.on("mouseout", function () {
            if (lastPointerType === "touch") return;
            if (activeWard !== name) {
              geojsonLayer.resetStyle(layer);
            }
            hideHoverDebounced();
          });

          layer.on("click", function (e) {
            if (e.originalEvent) e.originalEvent._wardHandled = true;
            if (activeWard === name) {
              activeWard = null;
              geojsonLayer.resetStyle(layer);
              clearLayer("selection");
              return;
            }
            if (activeWard) {
              geojsonLayer.eachLayer(function (l) { geojsonLayer.resetStyle(l); });
            }
            activeWard = name;
            layer.setStyle({ weight: 4, opacity: 1 });
            layer.bringToFront();
            setLayer("selection", wardInfoHtml(name));
          });
        },
      }).addTo(map);

      map.fitBounds(geojsonLayer.getBounds(), { padding: [10, 10] });

      map.on("click", function (e) {
        if (!e.originalEvent._wardHandled && activeWard) {
          activeWard = null;
          geojsonLayer.eachLayer(function (l) { geojsonLayer.resetStyle(l); });
          clearLayer("selection");
        }
      });
    });
})();
