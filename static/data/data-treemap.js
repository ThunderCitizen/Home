// D3 treemap for /data. Outer rectangle per signer, inner tiles per
// pack, sized by row count. Color maps to unit_kind via the site's
// --status-* tokens (light + dark modes themed upstream). No per-unit
// CSS variables — the token set is already accessible via ThemeColors.
//
// Ships one IIFE that self-installs on DOMContentLoaded; follows the
// pattern established by static/budget/budget-sankey.js.

(function () {
  'use strict';

  var container, svgEl, infoBarEl, payload;
  var resizeObs, resizeTimer = null;
  var lockedPackID = null; // populated from click; kept across hovers

  function init() {
    svgEl = document.getElementById('data-treemap');
    infoBarEl = document.getElementById('data-info-bar');
    var payloadEl = document.getElementById('data-treemap-data');
    if (!svgEl || !payloadEl) return;

    container = svgEl.parentElement;
    try {
      payload = JSON.parse(payloadEl.textContent || '{}');
    } catch (e) {
      return;
    }
    if (!payload.signers || !payload.signers.length) {
      svgEl.innerHTML = '<text x="50%" y="50%" text-anchor="middle" fill="currentColor" opacity="0.55" font-size="11">No packs to visualise yet.</text>';
      return;
    }

    render();

    resizeObs = new ResizeObserver(function () {
      if (resizeTimer) clearTimeout(resizeTimer);
      resizeTimer = setTimeout(render, 120);
    });
    resizeObs.observe(container);

    // Refresh colors when the user toggles theme — ThemeColors cache
    // invalidates on that event, so simply re-render.
    window.addEventListener('theme-changed', render);
    if (window.matchMedia) {
      window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', render);
    }
  }

  function unitColor(kind, tc) {
    switch (kind) {
      case 'budget_year':  return tc.statusWarn;
      case 'council_term': return tc.statusInfo;
      case 'transit_day':  return tc.statusOk;
      default:             return tc.statusMuted;
    }
  }

  function mix(hex, withHex, alpha) {
    // Fallback alpha mix when color-mix is unavailable in SVG fill.
    // Parses "#rrggbb" only — everything in the theme is that shape.
    function parse(h) {
      h = h.replace('#', '');
      if (h.length === 3) h = h.split('').map(function (c) { return c + c; }).join('');
      return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
    }
    var a = parse(hex || '#666666');
    var b = parse(withHex || '#000000');
    var r = Math.round(a[0] * alpha + b[0] * (1 - alpha));
    var g = Math.round(a[1] * alpha + b[1] * (1 - alpha));
    var bl = Math.round(a[2] * alpha + b[2] * (1 - alpha));
    return 'rgb(' + r + ',' + g + ',' + bl + ')';
  }

  function render() {
    if (!window.d3) return;
    var tc = window.ThemeColors ? window.ThemeColors() : {};
    var width = container.clientWidth || 640;
    // Use the computed height from CSS aspect-ratio to avoid layout
    // shift. Fall back to a 16:9 ratio if the browser hasn't applied
    // the rule yet.
    var height = container.clientHeight || Math.round(width * 9 / 16);
    if (height < 320) height = 320;

    svgEl.setAttribute('viewBox', '0 0 ' + width + ' ' + height);
    svgEl.setAttribute('width', width);
    svgEl.setAttribute('height', height);
    svgEl.innerHTML = '';

    var root = d3.hierarchy({
      children: payload.signers.map(function (s) {
        return {
          name: s.signer_file,
          short: s.signer_short,
          fp: s.signer_fp,
          hasError: s.has_error,
          children: s.packs.map(function (p) {
            return Object.assign({ name: p.pack_id }, p);
          })
        };
      })
    }).sum(function (d) {
      return d.total_rows || 0;
    }).sort(function (a, b) {
      return (b.value || 0) - (a.value || 0);
    });

    d3.treemap()
      .size([width, height])
      .paddingOuter(6)
      .paddingTop(28)
      .paddingInner(3)
      .round(true)(root);

    var svg = d3.select(svgEl);
    var g = svg.append('g');

    // Signer outer rectangles + header labels.
    var signers = root.children || [];
    var signerGroups = g.selectAll('g.signer')
      .data(signers)
      .enter().append('g')
      .attr('class', 'data-treemap-signer');

    signerGroups.append('rect')
      .attr('x', function (d) { return d.x0; })
      .attr('y', function (d) { return d.y0; })
      .attr('width', function (d) { return Math.max(0, d.x1 - d.x0); })
      .attr('height', function (d) { return Math.max(0, d.y1 - d.y0); })
      .attr('fill', tc.surfaceDark || '#111')
      .attr('fill-opacity', 0.08)
      .attr('stroke', function (d) { return d.data.hasError ? (tc.statusError || '#c00') : (tc.accent || '#888'); })
      .attr('stroke-width', 1);

    signerGroups.append('text')
      .attr('x', function (d) { return d.x0 + 10; })
      .attr('y', function (d) { return d.y0 + 18; })
      .attr('fill', tc.accent || '#888')
      .attr('font-size', 11)
      .attr('font-weight', 700)
      .attr('letter-spacing', '0.08em')
      .attr('font-family', 'var(--font-mono)')
      .text(function (d) {
        var name = (d.data.name || '').toUpperCase();
        var short = d.data.short ? (' · ' + d.data.short) : '';
        return truncate(name + short, Math.floor((d.x1 - d.x0 - 16) / 7));
      });

    // Pack tiles.
    var leaves = root.leaves();
    var tiles = g.selectAll('g.pack')
      .data(leaves)
      .enter().append('g')
      .attr('class', 'data-treemap-pack')
      .attr('tabindex', 0)
      .attr('role', 'button')
      .attr('aria-label', function (d) {
        return d.data.pack_id + ', ' + d.data.unit_label + ', ' + fmtNumber(d.data.total_rows) + ' rows';
      })
      .style('cursor', 'pointer');

    tiles.append('rect')
      .attr('x', function (d) { return d.x0; })
      .attr('y', function (d) { return d.y0; })
      .attr('width', function (d) { return Math.max(0, d.x1 - d.x0); })
      .attr('height', function (d) { return Math.max(0, d.y1 - d.y0); })
      .attr('fill', function (d) { return mix(unitColor(d.data.unit_kind, tc), tc.surfaceDark || '#000', 0.62); })
      .attr('stroke', function (d) { return unitColor(d.data.unit_kind, tc); })
      .attr('stroke-width', 0.75)
      .attr('rx', 2)
      .attr('ry', 2)
      .style('opacity', 0)
      .transition()
      .duration(280)
      .delay(function (d, i) { return 40 + i * 35; })
      .style('opacity', 1);

    // Scanline overlay on each tile — subtle CRT nod, matches the rest
    // of the site. One <rect> with a pattern fill would be cheaper but
    // the pattern element is overkill for this scale.
    tiles.append('rect')
      .attr('x', function (d) { return d.x0; })
      .attr('y', function (d) { return d.y0; })
      .attr('width', function (d) { return Math.max(0, d.x1 - d.x0); })
      .attr('height', function (d) { return Math.max(0, d.y1 - d.y0); })
      .attr('fill', 'url(#data-scanlines)')
      .attr('pointer-events', 'none')
      .attr('opacity', 0.35);

    // Labels — only when the tile is big enough.
    tiles.filter(function (d) {
      return (d.x1 - d.x0) > 70 && (d.y1 - d.y0) > 38;
    }).each(function (d) {
      var sel = d3.select(this);
      var tx = d.x0 + 8;
      var ty = d.y0 + 16;
      sel.append('text')
        .attr('x', tx)
        .attr('y', ty)
        .attr('fill', unitColor(d.data.unit_kind, tc))
        .attr('font-size', 11)
        .attr('font-weight', 700)
        .attr('font-family', 'var(--font-mono)')
        .text(truncate(d.data.pack_id, Math.floor((d.x1 - d.x0 - 16) / 7)));
      sel.append('text')
        .attr('x', tx)
        .attr('y', ty + 14)
        .attr('fill', tc.termFgDim || '#999')
        .attr('font-size', 10)
        .attr('font-family', 'var(--font-mono)')
        .text(d.data.unit_label);
      if ((d.y1 - d.y0) > 58) {
        sel.append('text')
          .attr('x', tx)
          .attr('y', ty + 28)
          .attr('fill', tc.termFgDim || '#999')
          .attr('font-size', 10)
          .attr('font-family', 'var(--font-mono)')
          .text(fmtNumber(d.data.total_rows) + ' rows · ' + d.data.dataset_count + ' ds');
      }
    });

    // Shared scanline pattern — reuse existing terminal scanline density.
    var defs = svg.append('defs');
    var pattern = defs.append('pattern')
      .attr('id', 'data-scanlines')
      .attr('patternUnits', 'userSpaceOnUse')
      .attr('width', 3)
      .attr('height', 3);
    pattern.append('rect').attr('width', 3).attr('height', 3).attr('fill', 'transparent');
    pattern.append('line').attr('x1', 0).attr('y1', 2).attr('x2', 3).attr('y2', 2)
      .attr('stroke', tc.surfaceDark || '#000').attr('stroke-width', 0.6).attr('opacity', 0.25);

    tiles.on('mouseenter', function (event, d) {
      showInfo(d.data);
      d3.select(this).select('rect').attr('fill', mix(unitColor(d.data.unit_kind, tc), tc.surfaceDark || '#000', 0.88));
    });
    tiles.on('mouseleave', function (event, d) {
      // Revert the hover fill but keep any locked selection visible
      // in the info bar — only clear when nothing is locked.
      d3.select(this).select('rect').attr('fill', mix(unitColor(d.data.unit_kind, tc), tc.surfaceDark || '#000', 0.62));
      if (!lockedPackID) hideInfo();
      else restoreLocked();
    });
    tiles.on('click', function (event, d) {
      event.stopPropagation();
      lockedPackID = d.data.pack_id;
      showInfo(d.data, true);
      jumpTo(d.data.pack_id);
    });
    tiles.on('keydown', function (event, d) {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        lockedPackID = d.data.pack_id;
        showInfo(d.data, true);
        jumpTo(d.data.pack_id);
      }
    });
    tiles.on('focus', function (event, d) { showInfo(d.data); });
    tiles.on('blur', function () {
      if (!lockedPackID) hideInfo();
    });

    // Click outside any tile clears the locked selection.
    svgEl.addEventListener('click', function () {
      lockedPackID = null;
      hideInfo();
    });
  }

  function showInfo(data, locked) {
    if (!infoBarEl) return;
    infoBarEl.innerHTML = infoMarkup(data);
    infoBarEl.classList.add('info-bar-visible');
    infoBarEl.classList.toggle('info-bar-locked', !!locked);
  }

  function restoreLocked() {
    if (!lockedPackID || !payload) return;
    var pack = findPack(lockedPackID);
    if (pack) showInfo(pack, true);
  }

  function findPack(id) {
    for (var i = 0; i < payload.signers.length; i++) {
      var packs = payload.signers[i].packs || [];
      for (var j = 0; j < packs.length; j++) {
        if (packs[j].pack_id === id) return packs[j];
      }
    }
    return null;
  }

  function hideInfo() {
    if (!infoBarEl) return;
    infoBarEl.classList.remove('info-bar-visible', 'info-bar-locked');
  }

  function infoMarkup(d) {
    var parts = [
      '<span class="data-info-id"><code>' + escapeHtml(d.pack_id) + '</code></span>',
      '<span class="data-info-unit">' + escapeHtml(d.unit_label) + '</span>',
      '<span class="data-info-count">' + fmtNumber(d.total_rows) + ' rows · ' + d.dataset_count + ' datasets</span>',
      '<span class="data-info-applied">Applied ' + escapeHtml(d.applied_at) + '</span>'
    ];
    if (d.merkle_short) {
      parts.push('<span class="data-info-merkle">Merkle <code>' + escapeHtml(d.merkle_short) + '</code></span>');
    }
    if (d.last_error) {
      parts.push('<span class="data-info-error">' + escapeHtml(d.last_error) + '</span>');
    }
    return parts.join('');
  }

  function jumpTo(packID) {
    var target = document.getElementById('pack-' + packID);
    if (!target) return;
    target.scrollIntoView({ behavior: 'smooth', block: 'center' });
    target.classList.remove('data-pack-flash');
    // force reflow so the animation restarts even on repeat clicks
    void target.offsetWidth;
    target.classList.add('data-pack-flash');
  }

  function fmtNumber(n) {
    if (n == null) return '0';
    return Number(n).toLocaleString();
  }

  function truncate(s, max) {
    if (!s || s.length <= max) return s || '';
    if (max <= 1) return '';
    return s.slice(0, Math.max(1, max - 1)) + '…';
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
