(function () {
  "use strict";

  const el = document.getElementById("election-ward-map");
  if (!el || !window.L) return;

  const colors = window.ThunderWardColors || {};
  const tc = ThemeColors();
  let map;
  let geojsonLayer;
  const wardCategory = document.getElementById("election-category-ward");
  const mapDetails = document.querySelector(".election-ward-map-details");
  const wardLayerButton = mapDetails ? mapDetails.querySelector("[data-layer='wards']") : null;
  let wardsVisible = wardLayerButton ? wardLayerButton.classList.contains("active") : true;

  document.querySelectorAll(".election-radio-card-ward-choice").forEach(function (card) {
    const color = colors[card.dataset.wardName];
    if (color) card.style.setProperty("--election-category-color", color);
  });

  function chooseWard(name) {
    const input = document.getElementById("election-choice-ward-" + name.toLowerCase().replaceAll(" ", "-"));
    const panel = document.getElementById("election-panel-ward-" + name.toLowerCase().replaceAll(" ", "-"));
    if (!input || !panel) return;

    input.checked = true;
    input.dispatchEvent(new Event("change", { bubbles: true }));
    window.requestAnimationFrame(function () {
      panel.scrollIntoView({ behavior: "smooth", block: "start" });
    });
  }

  function setLayerVisibility(layer, visible) {
    if (!map || !layer) return;
    if (visible && !map.hasLayer(layer)) layer.addTo(map);
    if (!visible && map.hasLayer(layer)) map.removeLayer(layer);
  }

  function wireLayerToggle(button, getVisible, setVisible, getLayer) {
    if (!button) return;
    button.setAttribute("aria-pressed", String(getVisible()));
    button.addEventListener("click", function () {
      const visible = !getVisible();
      setVisible(visible);
      button.classList.toggle("active", visible);
      button.setAttribute("aria-pressed", String(visible));
      setLayerVisibility(getLayer(), visible);
    });
  }

  wireLayerToggle(
    wardLayerButton,
    function () { return wardsVisible; },
    function (visible) { wardsVisible = visible; },
    function () { return geojsonLayer; }
  );
  function initialiseMap() {
    if (map) return;

    map = L.map(el, { zoomControl: true });
    el._leafletMap = map;
    ThunderMapTiles().addTo(map);

    fetch("/static/councillors/thunder-bay-wards.geojson")
      .then(function (response) { return response.json(); })
      .then(function (data) {
        geojsonLayer = L.geoJSON(data, {
          style: function (feature) {
            return {
              color: colors[feature.properties.name] || tc.statusMuted,
              weight: 2,
              fillOpacity: 0.45,
            };
          },
          onEachFeature: function (feature, layer) {
            const name = feature.properties.name;
            layer.bindTooltip(name, { permanent: true, direction: "center", className: "ward-label" });
            layer.on("mouseover", function () { layer.setStyle({ weight: 4, fillOpacity: 0.6 }); });
            layer.on("mouseout", function () { geojsonLayer.resetStyle(layer); });
            layer.on("click", function () { chooseWard(name); });
          },
        });
        setLayerVisibility(geojsonLayer, wardsVisible);
        map.fitBounds(geojsonLayer.getBounds(), { padding: [10, 10] });
      });
  }

  if (wardCategory) {
    wardCategory.addEventListener("change", function () {
      if (!wardCategory.checked) return;
      if (!mapDetails || !mapDetails.open) return;
      initialiseMap();
      if (!geojsonLayer) return;
      window.setTimeout(function () {
        map.invalidateSize();
        map.fitBounds(geojsonLayer.getBounds(), { padding: [10, 10] });
      }, 0);
    });
  }

  if (mapDetails) {
    mapDetails.addEventListener("toggle", function () {
      if (!mapDetails.open || !wardCategory || !wardCategory.checked) return;
      initialiseMap();
      window.setTimeout(function () {
        if (!geojsonLayer) return;
        map.invalidateSize();
        map.fitBounds(geojsonLayer.getBounds(), { padding: [10, 10] });
      }, 0);
    });
  }
})();
