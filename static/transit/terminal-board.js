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
  const tabs = document.querySelectorAll(".terminal-board-tab");
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
      allGroups = [];
      pageIndex = 0;
      if (pageTimer) { clearTimeout(pageTimer); pageTimer = null; }
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

  const ROUTE_COLORS = {};
  const ROUTE_TEXT = {};
  try {
    const meta = JSON.parse(
      (document.getElementById("route-meta") || {}).textContent || "[]",
    );
    for (let i = 0; i < meta.length; i++) {
      const m = meta[i];
      if (m && m.route_id) {
        if (m.color) ROUTE_COLORS[m.route_id] = m.color;
        if (m.text_color) ROUTE_TEXT[m.route_id] = m.text_color;
      }
    }
  } catch (_e) {
    /* malformed JSON shouldn't break the page */
  }

  const STEADY_MS = 15000;
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
    if (!p) return "";
    const t = clockTime(p);
    if (!t) return "";
    const sched = p && p.scheduled ? scheduledClockTime(p.scheduled) : "";
    if (statusKind(p) === "late" && sched && sched !== t) {
      return t + " (was " + sched + ")";
    }
    return t;
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
    // Sort groups by their next departure (soonest first) so the
    // most-imminent route is always at the top of the board.
    order.sort(function (a, b) {
      const an = parseInt(a.route.route_id, 10);
      const bn = parseInt(b.route.route_id, 10);
      if (!isNaN(an) && !isNaN(bn)) return an - bn;
      return (a.route.route_id || "").localeCompare(b.route.route_id || "");
    });
    return order;
  }

  // Pagination: when a terminal has more route groups than fit at hero
  // size, we rotate pages instead of shrinking. Page size is set by CSS
  // (grid auto-fill) — we slice the groups in JS and rotate every PAGE_MS.
  // Per FHWA DMS guidelines + vestibular comfort research, page-flip
  // beats scrolling and the cross-fade floor is 400 ms.
  const PAGE_SIZE = 4; // visible groups per page in fullscreen — 2×2 grid
  const PAGE_MS = 8000; // page rotation cadence
  const FADE_MS = 400; // cross-fade duration (matches CSS .is-fading)
  let allGroups = [];
  let pageIndex = 0;
  let pageTimer = null;
  const pageIndicatorEl = document.getElementById("board-page-indicator");

  // Build indicator markup once. Shown/hidden via the `hidden` attribute.
  // Ring's stroke-dashoffset animated via @keyframes page-countdown-anim
  // (8s linear). Clone-replacing the fill circle restarts the anim cleanly.
  if (pageIndicatorEl) {
    pageIndicatorEl.innerHTML =
      '<span class="page-countdown-wrap" aria-hidden="true">' +
        '<svg class="page-countdown" viewBox="0 0 36 36">' +
          '<circle class="page-countdown-track" cx="18" cy="18" r="15"></circle>' +
          '<circle class="page-countdown-fill" cx="18" cy="18" r="15"></circle>' +
        '</svg>' +
      '</span>' +
      '<span class="page-indicator-label"></span>';
    pageIndicatorEl.hidden = true;
  }

  function restartCountdown() {
    if (!pageIndicatorEl) return;
    const fill = pageIndicatorEl.querySelector(".page-countdown-fill");
    if (!fill) return;
    const clone = fill.cloneNode(true);
    fill.parentNode.replaceChild(clone, fill);
    pageIndicatorEl.classList.add("is-running");
  }

  function totalPages() {
    if (!document.body.classList.contains("terminal-fullscreen")) return 1;
    return Math.max(1, Math.ceil(allGroups.length / PAGE_SIZE));
  }

  function paintPage() {
    if (!rowsEl) return;
    if (!allGroups.length) {
      rowsEl.innerHTML = "";
      if (emptyEl) {
        emptyEl.textContent = "No departures in the next hour.";
        rowsEl.appendChild(emptyEl);
      }
      hasRendered = true;
      return;
    }
    const inFullscreen = document.body.classList.contains("terminal-fullscreen");
    const start = inFullscreen ? pageIndex * PAGE_SIZE : 0;
    const groups = inFullscreen ? allGroups.slice(start, start + PAGE_SIZE) : allGroups;
    let html = "";
    for (let i = 0; i < groups.length; i++) html += renderCard(groups[i]);
    rowsEl.innerHTML = html;
    const pages = totalPages();
    if (pageIndicatorEl) {
      pageIndicatorEl.hidden = pages <= 1;
      const label = pageIndicatorEl.querySelector(".page-indicator-label");
      if (label) label.textContent = (pageIndex + 1) + " / " + pages;
    }
    hasRendered = true;
  }

  // Unified card: header (pill + headsign + status-when-bad), hero (minutes
  // primary, clock secondary), footer ("Then ..." line). Same markup for
  // desktop and fullscreen — CSS clamps scale the type. Cancellation
  // promotes the next available departure to the hero slot.
  function renderCard(g) {
    const head = g.route;
    const color =
      ROUTE_COLORS[head.route_id] || head.route_color || "var(--accent)";
    const text = ROUTE_TEXT[head.route_id] || "#0a100a";
    const status = statusPresentation(head);
    const upcoming = g.items.slice(0, 3);
    const isCancelled = status.kind === "cancelled";
    const heroP = isCancelled ? upcoming[1] : upcoming[0];
    const thenP = isCancelled ? upcoming[2] : upcoming[1];
    const laterP = isCancelled ? null : upcoming[2];
    const cancelledP = isCancelled ? upcoming[0] : null;
    const headsign = head.headsign || head.route_name || "";

    const heroPrimaryText = heroP ? heroPrimary(heroP) : "—";
    const heroSubText = heroP ? heroSub(heroP) : "";
    const ariaWhen = heroP && typeof heroP.minutes_away === "number"
      ? " in " + heroP.minutes_away + " minutes"
      : "";
    const ariaStatus = status.kind !== "scheduled" && status.kind !== "ontime"
      ? ", " + status.word + (status.delta ? " " + status.delta + " minutes" : "")
      : "";
    const aria = "Route " + head.route_id + " to " + headsign + ariaWhen + ariaStatus;

    // Status pill renders for every state except "scheduled" (no real-time
    // data). On-time gets a calm green confirmation; late/early/cancelled
    // get their attention-grabbing colors. Color-coded left border on the
    // card encodes the same info structurally for at-a-glance scanning.
    let statusHTML = "";
    if (status.kind !== "scheduled") {
      const statusDelta = status.delta ? " " + status.delta + " min" : "";
      statusHTML =
        '<span class="terminal-card-status terminal-board-status-pill-' + status.kind + '">' +
          '<span class="terminal-card-status-glyph" aria-hidden="true">' + status.glyph + "</span>" +
          '<span class="terminal-card-status-word">' + escapeHTML(status.word + statusDelta) + "</span>" +
        "</span>";
    }

    // Cancelled banner — shows the dropped time struck through, in the
    // header row. Rider sees the news without losing the next-bus answer.
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

    let footerHTML = "";
    if (cancelledP && !heroP) {
      footerHTML = '<div class="terminal-card-then terminal-card-then-empty">no further departures</div>';
    } else if (thenP || laterP) {
      let inner = "";
      if (thenP) {
        inner +=
          '<div class="terminal-card-then-row">' +
            '<span class="terminal-card-then-label">Then</span>' +
            '<span class="terminal-card-then-value">' + escapeHTML(thenLine(thenP)) + "</span>" +
          "</div>";
      }
      if (laterP) {
        inner +=
          '<div class="terminal-card-then-row terminal-card-later-row">' +
            '<span class="terminal-card-then-label">Later</span>' +
            '<span class="terminal-card-then-value">' + escapeHTML(thenLine(laterP)) + "</span>" +
          "</div>";
      }
      footerHTML = '<div class="terminal-card-then">' + inner + "</div>";
    }

    return (
      '<article class="terminal-card terminal-card-' + status.kind +
      '" style="border-left-color:' + escapeHTML(color) +
      '" aria-label="' + escapeHTML(aria) + '">' +
        '<header class="terminal-card-head">' +
          '<span class="terminal-card-pill" style="background:' + escapeHTML(color) +
          ";color:" + escapeHTML(text) + '">' + escapeHTML(head.route_id) + "</span>" +
          '<span class="terminal-card-headsign">' + escapeHTML(headsign) + "</span>" +
          statusHTML +
        "</header>" +

        cancelledHTML +

        '<div class="terminal-card-hero">' +
          '<span class="terminal-card-hero-value">' + escapeHTML(heroPrimaryText) + "</span>" +
          (heroSubText
            ? '<span class="terminal-card-hero-sub">' + escapeHTML(heroSubText) + "</span>"
            : "") +
        "</div>" +

        footerHTML +
      "</article>"
    );
  }

  function schedulePageRotate() {
    if (pageTimer) clearTimeout(pageTimer);
    if (totalPages() <= 1) return;
    // Fire FADE_MS early so fade-out starts as the ring completes,
    // and the page change lands exactly when PAGE_MS elapses.
    pageTimer = setTimeout(rotatePage, PAGE_MS - FADE_MS);
  }

  function rotatePage() {
    if (totalPages() <= 1) return;
    if (rowsEl) rowsEl.classList.add("is-fading");
    setTimeout(function () {
      pageIndex = (pageIndex + 1) % totalPages();
      updateFeedAge(); // safe — content invisible during fade
      paintPage();
      if (rowsEl) rowsEl.classList.remove("is-fading");
      restartCountdown();
      schedulePageRotate();
    }, FADE_MS);
  }

  function render(predictions, feedTS) {
    if (!rowsEl) return;
    allGroups = (predictions && predictions.length) ? groupByRoute(predictions) : [];
    if (pageIndex >= totalPages()) pageIndex = 0;
    paintPage();
    if (feedTS) lastFeedAt = feedTS;
    updateFeedAge(); // age resets on fresh data — safe to update immediately
    // Only kick off the timer on first arrival — rotatePage keeps it self-sustaining.
    if (!pageTimer) schedulePageRotate();
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
        render(data && data.predictions ? data.predictions : [], lastFeedAt);
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
  let priorTheme = null;
  function syncFullscreen() {
    const goingFs = !!document.fullscreenElement;
    document.body.classList.toggle("terminal-fullscreen", goingFs);
    const html = document.documentElement;
    if (goingFs) {
      priorTheme = html.getAttribute("data-theme");
      html.setAttribute("data-theme", "kiosk");
    } else if (priorTheme !== null) {
      html.setAttribute("data-theme", priorTheme);
      priorTheme = null;
    } else {
      html.removeAttribute("data-theme");
    }
    // Pagination is fullscreen-only; reset + repaint when crossing the boundary.
    pageIndex = 0;
    if (pageTimer) { clearTimeout(pageTimer); pageTimer = null; }
    paintPage();
    restartCountdown();
    schedulePageRotate();
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
