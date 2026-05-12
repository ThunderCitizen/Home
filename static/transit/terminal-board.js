// Thunder Citizen — fullscreen terminal departures board.
//
// Self-pacing poll loop against /api/transit/stop/{id}/predictions.
// Goals: never go blank, never page on a backend hiccup. The board is
// stateless; a hard refresh restores everything from URL + last fetch.
//
// Backoff: 15s steady-state, exponential 1→2→4→…→30s on error, immediate
// resume on success. Visibility API pauses while hidden. Local clock
// ticks independently of the server so the time stays accurate even
// while the API is unreachable.
(function () {
  "use strict";

  const board = document.querySelector(".terminal-board");
  if (!board) return;

  let stopID = board.getAttribute("data-stop-id");
  if (!stopID) return;

  const stopNameEl = document.getElementById("board-stop-name");
  const tabs = document.querySelectorAll(".selector-bar-btn[data-stop-id]");
  tabs.forEach(function (tab) {
    tab.addEventListener("click", function () {
      const newID = tab.getAttribute("data-stop-id");
      if (!newID || newID === stopID) return;
      tabs.forEach(function (t) {
        const active = t === tab;
        t.classList.toggle("active", active);
        t.setAttribute("aria-selected", active ? "true" : "false");
      });
      stopID = newID;
      board.setAttribute("data-stop-id", newID);
      const newName = tab.getAttribute("data-stop-name") || "";
      board.setAttribute("data-stop-name", newName);
      if (stopNameEl) stopNameEl.textContent = newName;
      // Force a fresh fetch + clear last-good frame so the user sees
      // they're now looking at a different terminal, not the previous
      // one's cached predictions.
      hasRendered = false;
      lastFeedAt = null;
      consecutiveErrors = 0;
      groups = [];
      if (rowsEl) {
        rowsEl.innerHTML =
          '<div class="terminal-board-empty">Loading ' +
          escapeHTML(newName) +
          "…</div>";
      }
      if (pollTimer) {
        clearTimeout(pollTimer);
        pollTimer = null;
      }
      poll();
    });
  });

  const rowsEl = document.getElementById("board-rows");
  const emptyEl = document.getElementById("board-empty");
  const updatedEl = document.getElementById("board-updated");
  const clockEl = document.getElementById("board-clock");
  const clockFsEl = document.getElementById("board-clock-fs");

  const ROUTE_COLORS = (window.RouteMeta && window.RouteMeta.colors) || {};
  const ROUTE_TEXT = (window.RouteMeta && window.RouteMeta.texts) || {};

  const STEADY_MS = parseInt(board.getAttribute("data-poll-ms"), 10) || 15000;
  const BACKOFF_MIN = 1000;
  const BACKOFF_MAX = 30000;

  let lastFeedAt = null; // server-reported feed timestamp, ms epoch
  let lastFetchOK = 0; // local ms when we last got a 2xx
  let consecutiveErrors = 0;
  let inFlight = false;
  let pollTimer = null;
  let hasRendered = false;

function pad2(n) {
    n = String(n);
    return n.length < 2 ? "0" + n : n;
  }

  // Feed age — updated during page-turn fades (content invisible) and on
  // data arrival. For single-page / stale feeds, also fires on the 0-second
  // boundary so it advances at most once per minute, never mid-read.
  function updateFeedAge() {
    if (!updatedEl) return;
    if (!lastFeedAt) { updatedEl.style.visibility = "hidden"; return; }
    const ageMin = Math.floor((Date.now() - lastFeedAt) / 60000);
    if (ageMin >= 2) {
      updatedEl.textContent = "Updated " + ageMin + "m ago";
      updatedEl.style.visibility = "";
    } else {
      updatedEl.style.visibility = "hidden";
    }
  }

  function tickClock() {
    const now = new Date();
    const h = now.getHours() % 12 || 12;
    const timeStr = h + ":" + pad2(now.getMinutes()) + " " + (now.getHours() >= 12 ? "PM" : "AM");
    if (clockEl) clockEl.textContent = timeStr;
    if (clockFsEl) clockFsEl.textContent = timeStr;
    // Advance feed-age label only at minute boundaries — avoids mid-display flash.
    if (now.getSeconds() === 0) updateFeedAge();
  }
  setInterval(tickClock, 1000);
  tickClock();

  function escapeHTML(s) {
    return String(s == null ? "" : s).replace(/[&<>"']/g, function (c) {
      return (
        { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[
          c
        ] || c
      );
    });
  }

  function statusKind(p) {
    const s = (p.status || "").toLowerCase();
    if (s === "cancelled" || s === "canceled") return "cancelled";
    if (s.indexOf("late") !== -1) return "late";
    if (s.indexOf("early") !== -1) return "early";
    if (s === "on time") return "ontime";
    return "scheduled";
  }

  // Status redundancy: glyph + word + delta. Color is the third channel,
  // never the only one (WCAG 1.4.1 / persona requirement).
  function statusPresentation(p) {
    const kind = statusKind(p);
    const delta = typeof p.delay_seconds === "number"
      ? Math.max(1, Math.round(Math.abs(p.delay_seconds) / 60))
      : null;
    switch (kind) {
      case "cancelled":
        return { kind: kind, glyph: "✕", word: "Cancelled" };
      case "late":
        return { kind: kind, glyph: "▲", word: "Late", delta: delta };
      case "early":
        return { kind: kind, glyph: "▼", word: "Early", delta: delta };
      case "ontime":
        return { kind: kind, glyph: "●", word: "On time" };
      default:
        return { kind: kind, glyph: "◐", word: "Scheduled" };
    }
  }

  // Server formats predicted as "3:04 PM" (12h with suffix). If that ever
  // changes to 24h we transparently convert. Never re-derive AM/PM from a
  // 12-hour number — that's how we shipped 1:40 PM as 1:40 AM.
  function clockTime(p) {
    const raw = p && p.predicted;
    if (!raw) return "";
    const s = String(raw).trim();
    if (/\b(AM|PM|am|pm)\b/.test(s)) return s.toUpperCase();
    const m = /^(\d{1,2}):(\d{2})/.exec(s);
    if (!m) return s;
    let h = parseInt(m[1], 10);
    const mm = m[2];
    if (isNaN(h)) return s;
    const ampm = h >= 12 ? "PM" : "AM";
    h = h % 12;
    if (h === 0) h = 12;
    return h + ":" + mm + " " + ampm;
  }

  function etaMin(p) {
    const m = p.minutes_away;
    if (typeof m !== "number") return "";
    if (m <= 0) return "Now";
    if (m === 1) return "1 min";
    return m + " min";
  }

  // Hero number for a single departure — minutes-away as the primary value.
  // Per Caird & Hancock (2002), older adults parse "X min" ~30% faster than
  // "HH:MM" for sub-30-min windows. Falls back to clock time when minutes
  // data is missing.
  function heroPrimary(p) {
    if (!p) return "—";
    const m = p.minutes_away;
    if (typeof m !== "number") return clockTime(p) || "—";
    if (m <= 0) return "Now";
    if (m === 1) return "1 min";
    return m + " min";
  }

  // Sub line under the hero — clock time, plus "(was HH:MM)" when late and
  // the scheduled time differs from the predicted time.
  function heroSub(p) {
    if (!p) return { time: "", sched: "" };
    const t = clockTime(p);
    if (!t) return { time: "", sched: "" };
    const sched = p && p.scheduled ? scheduledClockTime(p.scheduled) : "";
    if (statusKind(p) === "late" && sched && sched !== t) {
      return { time: t, sched: sched };
    }
    return { time: t, sched: "" };
  }

  // Footer "Then" line: "29 min · 2:44 PM" or empty when no second item.
  function thenLine(p) {
    if (!p) return "";
    const min = etaMin(p);
    const t = clockTime(p);
    if (min && t) return min + " · " + t;
    return min || t;
  }

  // 12h converter for the scheduled string (same shape as predicted).
  function scheduledClockTime(raw) {
    const s = String(raw || "").trim();
    if (!s) return "";
    if (/\b(AM|PM|am|pm)\b/.test(s)) return s.toUpperCase();
    const m = /^(\d{1,2}):(\d{2})/.exec(s);
    if (!m) return s;
    let h = parseInt(m[1], 10);
    const mm = m[2];
    if (isNaN(h)) return s;
    const ampm = h >= 12 ? "PM" : "AM";
    h = h % 12;
    if (h === 0) h = 12;
    return h + ":" + mm + " " + ampm;
  }

  // groupByRoute folds the flat prediction list into one row per
  // (route_id + headsign) — terminals see both inbound and outbound
  // legs of the same route, so we keep them as separate rows. Within a
  // group the upcoming departures are kept in arrival order so the
  // first chip is the next bus, the second is the one after, etc.
  function groupByRoute(predictions) {
    const groups = new Map();
    const order = [];
    for (let i = 0; i < predictions.length; i++) {
      const p = predictions[i];
      const key = (p.route_id || "") + "\t" + (p.headsign || p.route_name || "");
      let g = groups.get(key);
      if (!g) {
        g = { key: key, route: p, items: [] };
        groups.set(key, g);
        order.push(g);
      }
      g.items.push(p);
    }
    // Sort groups by route number for a stable, predictable card order.
    order.sort(function (a, b) {
      const an = parseInt(a.route.route_id, 10);
      const bn = parseInt(b.route.route_id, 10);
      if (!isNaN(an) && !isNaN(bn)) return an - bn;
      return (a.route.route_id || "").localeCompare(b.route.route_id || "");
    });
    return order;
  }

  let groups = [];

  function slotHasShutter(card, selector) {
    var el = card.querySelector(selector);
    return !!(el && el.querySelector(".terminal-card-shutter-panel"));
  }

  function snapshotSlots(card) {
    return {
      hero:  slotHasShutter(card, ".terminal-card-hero"),
      then:  slotHasShutter(card, ".terminal-card-foot-cell:not(.terminal-card-foot-cell-later)"),
      later: slotHasShutter(card, ".terminal-card-foot-cell-later"),
    };
  }

  // Inject a ghost shutter panel that plays the open animation then removes itself.
  function injectOpenShutter(container, delayMs) {
    if (!container) return;
    var panel = document.createElement("div");
    panel.className = "terminal-card-shutter-panel shutter-opening";
    if (delayMs) panel.style.animationDelay = delayMs + "ms";
    container.appendChild(panel);
    panel.addEventListener("animationend", function () { panel.remove(); });
  }

  function paintPage() {
    if (!rowsEl) return;
    if (!groups.length) {
      rowsEl.innerHTML = "";
      if (emptyEl) {
        emptyEl.textContent = "No departures in the next hour.";
        rowsEl.appendChild(emptyEl);
      }
      hasRendered = true;
      return;
    }

    // Snapshot per-slot shutter state before blowing away the DOM.
    var snapshot = {};
    rowsEl.querySelectorAll(".terminal-card[data-card-key]").forEach(function (card) {
      snapshot[card.getAttribute("data-card-key")] = snapshotSlots(card);
    });

    var html = "";
    for (var i = 0; i < groups.length; i++) html += renderCard(groups[i]);
    rowsEl.innerHTML = html;

    rowsEl.querySelectorAll(".terminal-card[data-card-key]").forEach(function (card) {
      var key = card.getAttribute("data-card-key");
      var prev = snapshot[key];
      if (!prev) return; // new card — animate normally

      var heroPanel  = card.querySelector(".terminal-card-hero .terminal-card-shutter-panel");
      var thenPanel  = card.querySelector(".terminal-card-foot-cell:not(.terminal-card-foot-cell-later) .terminal-card-shutter-panel");
      var laterPanel = card.querySelector(".terminal-card-foot-cell-later .terminal-card-shutter-panel");

      // Slot still shuttered → suppress close animation (already closed).
      if (prev.hero  && heroPanel)  heroPanel.classList.add("shutter-inert");
      if (prev.then  && thenPanel)  thenPanel.classList.add("shutter-inert");
      if (prev.later && laterPanel) laterPanel.classList.add("shutter-inert");

      // Slot was shuttered but now has a prediction → play open animation.
      if (prev.hero  && !heroPanel)  injectOpenShutter(card.querySelector(".terminal-card-hero"), 0);
      if (prev.then  && !thenPanel)  injectOpenShutter(card.querySelector(".terminal-card-foot-cell:not(.terminal-card-foot-cell-later)"), 120);
      if (prev.later && !laterPanel) injectOpenShutter(card.querySelector(".terminal-card-foot-cell-later"), 240);
    });

    hasRendered = true;
  }

  // Three-row grid: header (pill + headsign), hero (status pill + minutes
  // + clock sub + cancelled banner), footer (Then | Later cells). Header is
  // a content-sized commitment so the hero can never squash it. Hero is
  // bounded on both axes so it can never overflow the card.
  function renderCard(g) {
    const head = g.route;
    const color =
      ROUTE_COLORS[head.route_id] || head.route_color || "var(--accent)";
    const text = ROUTE_TEXT[head.route_id] || head.route_text_color || "#0a100a";
    const status = statusPresentation(head);
    const upcoming = g.items.slice(0, 3);
    const isCancelled = status.kind === "cancelled";
    const heroP = isCancelled ? upcoming[1] : upcoming[0];
    const thenP = isCancelled ? upcoming[2] : upcoming[1];
    const laterP = isCancelled ? null : upcoming[2];
    const cancelledP = isCancelled ? upcoming[0] : null;
    const headsign = head.headsign || head.route_name || "";

    const isNoService = g.items.length === 0;
    const heroPrimaryText = heroP ? heroPrimary(heroP) : "—";
    const heroSubData = heroP ? heroSub(heroP) : { time: "", sched: "" };
    const ariaWhen = heroP && typeof heroP.minutes_away === "number"
      ? " in " + heroP.minutes_away + " minutes"
      : "";
    const ariaStatus = status.kind !== "scheduled" && status.kind !== "ontime"
      ? ", " + status.word + (status.delta ? " " + status.delta + " minutes" : "")
      : "";
    const aria = isNoService
      ? "Route " + head.route_id + " to " + headsign + ", no more service"
      : "Route " + head.route_id + " to " + headsign + ariaWhen + ariaStatus;

    // Status pill: emitted for every state except "scheduled".
    let statusHTML = "";
    if (status.kind !== "scheduled") {
      const statusDelta = status.delta ? " " + status.delta + " min" : "";
      statusHTML =
        '<span class="terminal-card-status terminal-board-status-pill-' + status.kind + '">' +
          '<span class="terminal-card-status-glyph" aria-hidden="true">' + status.glyph + "</span>" +
          '<span class="terminal-card-status-word">' + escapeHTML(status.word + statusDelta) + "</span>" +
        "</span>";
    }

    // Cancelled banner — dropped time, struck through.
    let cancelledHTML = "";
    if (cancelledP) {
      const t = clockTime(cancelledP);
      cancelledHTML =
        '<span class="terminal-card-cancelled-line">' +
          '<span class="terminal-card-cancelled-glyph" aria-hidden="true">✕</span>' +
          '<span class="terminal-card-cancelled-time">' + escapeHTML(t || "—") + "</span>" +
          '<span class="terminal-card-cancelled-word">cancelled</span>' +
        "</span>";
    }

    // A shutter panel slides down over an empty slot to signal no service.
    // delayMs staggers the close cascade: Later→Then→Hero.
    function shutterPanel(delayMs) {
      return '<div class="terminal-card-shutter-panel"' +
        (delayMs ? ' style="animation-delay:' + delayMs + 'ms"' : "") +
        '></div>';
    }

    // Footer cells — layout always rendered; empty slots get a shutter on top.
    function footCell(p, label, extraClass, shutterDelay) {
      const min = p ? (etaMin(p) || "—") : "—";
      const cls = "terminal-card-foot-cell" +
        (!p ? " terminal-card-foot-cell-empty" : "") +
        (extraClass ? " " + extraClass : "");
      return (
        '<span class="' + cls + '">' +
          '<span class="terminal-card-foot-label">' + label + "</span>" +
          '<span class="terminal-card-foot-value">' + escapeHTML(min) + "</span>" +
          (!p ? shutterPanel(shutterDelay) : "") +
        "</span>"
      );
    }

    const heroMetaHTML = heroSubData.time
      ? '<div class="terminal-card-hero-meta">' +
          '<span class="terminal-card-hero-sub">' + escapeHTML(heroSubData.time) + "</span>" +
        "</div>" +
        (heroSubData.sched
          ? '<span class="terminal-card-hero-sched">(sch: ' + escapeHTML(heroSubData.sched) + ")</span>"
          : "")
      : "";

    let footerHTML = "";
    if (cancelledP && !heroP) {
      footerHTML =
        '<div class="terminal-card-footer terminal-card-footer-empty">' +
          '<span class="terminal-card-foot-cell-empty-msg">no further departures</span>' +
        "</div>";
    } else {
      footerHTML =
        '<div class="terminal-card-footer">' +
          footCell(thenP, "Then", "", 120) +
          footCell(laterP, "Later", "terminal-card-foot-cell-later", 0) +
        "</div>";
    }

    // Hero — layout always rendered; shutter covers it last (140ms delay)
    // when there is no next departure. Skip hero shutter if we have a
    // cancelled banner — it already communicates the empty state.
    const heroShutter = (!heroP && !cancelledP) ? shutterPanel(240) : "";

    const bodyHTML =
      '<div class="terminal-card-body">' +
        '<div class="terminal-card-hero">' +
          heroMetaHTML +
          '<span class="terminal-card-hero-value">' + escapeHTML(heroPrimaryText) + "</span>" +
          cancelledHTML +
          heroShutter +
        "</div>" +
        footerHTML +
      "</div>";

    return (
      '<article class="terminal-card terminal-card-' + status.kind +
      (isNoService ? " terminal-card-no-service" : "") +
      '" data-card-key="' + escapeHTML(g.key) +
      '" style="border-left-color:' + escapeHTML(color) +
      '" aria-label="' + escapeHTML(aria) + '">' +
        '<header class="terminal-card-head">' +
          '<span class="terminal-card-pill" style="background:' + escapeHTML(color) +
          ";color:" + escapeHTML(text) + '">' + escapeHTML(head.route_id) + "</span>" +
          '<span class="terminal-card-headsign">' + escapeHTML(headsign) + "</span>" +
          statusHTML +
        "</header>" +
        bodyHTML +
      "</article>"
    );
  }

  function render(predictions, feedTS, expectedRoutes) {
    if (!rowsEl) return;
    groups = (predictions && predictions.length) ? groupByRoute(predictions) : [];


    // Add ghost groups for routes with scheduled service but no live predictions.
    if (expectedRoutes && expectedRoutes.length) {
      const seen = {};
      for (let i = 0; i < groups.length; i++) seen[groups[i].key] = true;
      for (let i = 0; i < expectedRoutes.length; i++) {
        const er = expectedRoutes[i];
        const key = (er.route_id || "") + "\t" + (er.headsign || "");
        if (seen[key]) continue;
        groups.push({
          key: key,
          route: {
            route_id: er.route_id,
            route_name: er.route_id,
            headsign: er.headsign,
            route_color: er.color,
            route_text_color: er.text_color,
            status: "Scheduled",
          },
          items: [],
        });
      }
      groups.sort(function (a, b) {
        const an = parseInt(a.route.route_id, 10);
        const bn = parseInt(b.route.route_id, 10);
        if (!isNaN(an) && !isNaN(bn)) return an - bn;
        return (a.route.route_id || "").localeCompare(b.route.route_id || "");
      });
    }

    paintPage();
    if (feedTS) lastFeedAt = feedTS;
    updateFeedAge();
  }

  function scheduleNext(delayMs) {
    if (pollTimer) clearTimeout(pollTimer);
    pollTimer = setTimeout(poll, delayMs);
  }

  function poll() {
    if (document.hidden) return; // resumed by visibilitychange
    if (inFlight) return;
    inFlight = true;
    const url = "/api/transit/stop/" + encodeURIComponent(stopID) + "/predictions";
    fetch(url, { credentials: "same-origin", cache: "no-store" })
      .then(function (resp) {
        if (!resp.ok) throw new Error("HTTP " + resp.status);
        return resp.json();
      })
      .then(function (data) {
        consecutiveErrors = 0;
        lastFetchOK = Date.now();
        const feedTS = data && data.updated_at ? Date.parse(data.updated_at) : null;
        if (feedTS && !isNaN(feedTS)) lastFeedAt = feedTS;
        render(
          data && data.predictions ? data.predictions : [],
          lastFeedAt,
          data && data.expected_routes ? data.expected_routes : [],
        );
        scheduleNext(STEADY_MS);
      })
      .catch(function () {
        consecutiveErrors++;
        const delay = Math.min(
          BACKOFF_MAX,
          BACKOFF_MIN * Math.pow(2, consecutiveErrors - 1),
        );
        scheduleNext(delay);
      })
      .then(function () {
        inFlight = false;
      });
  }

  // --- Fullscreen toggle ---
  // Browse mode keeps the user's chosen Thunder Citizen theme. Entering
  // fullscreen swaps to the kiosk theme (distance-legible amber-on-black);
  // exiting restores the prior theme. ESC exits fullscreen natively;
  // fullscreenchange keeps body class + theme in sync.
  function syncFullscreen() {
    const goingFs = !!document.fullscreenElement;
    document.body.classList.toggle("terminal-fullscreen", goingFs);
    paintPage();
  }
  var fsBtn = document.getElementById("board-fs-toggle");
  if (fsBtn) {
    fsBtn.addEventListener("click", function () {
      if (document.fullscreenElement) {
        document.exitFullscreen().catch(function () {});
      } else {
        document.documentElement.requestFullscreen().catch(function () {});
      }
    });
  }
  document.addEventListener("fullscreenchange", syncFullscreen);

  document.addEventListener("visibilitychange", function () {
    if (document.hidden) {
      if (pollTimer) {
        clearTimeout(pollTimer);
        pollTimer = null;
      }
    } else {
      poll();
    }
  });

  // Self-heal on long-running tabs: if the page has been backgrounded
  // long enough that the device suspended JS timers, kick a fresh fetch
  // when the user comes back via focus too (Safari fires focus but not
  // always visibilitychange after wake).
  window.addEventListener("focus", function () {
    if (Date.now() - lastFetchOK > STEADY_MS * 2) poll();
  });

  poll();
})();
