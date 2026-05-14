// Single-popover date-range picker for /transit/metrics and /transit/routes.
// Renders one month at a time with prev/next; the user clicks a start date,
// then an end date, and the page navigates to ?from=&to= immediately.
//
// Server contract: parseDateRange (handler.go) clamps to MinDate..MaxDate
// and swaps if from>to, so we don't need to enforce those invariants here.
// We do enforce them as UX (disabled days, second-click swap) to avoid the
// surprise of the server silently rewriting your selection.
(function () {
  "use strict";

  function parseISO(s) {
    if (!s) return null;
    var m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
    if (!m) return null;
    return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
  }

  function fmtISO(d) {
    var y = d.getFullYear();
    var m = String(d.getMonth() + 1).padStart(2, "0");
    var day = String(d.getDate()).padStart(2, "0");
    return y + "-" + m + "-" + day;
  }

  function dayDiff(a, b) {
    return Math.round((b - a) / 86400000);
  }

  function sameDay(a, b) {
    return a && b &&
      a.getFullYear() === b.getFullYear() &&
      a.getMonth() === b.getMonth() &&
      a.getDate() === b.getDate();
  }

  function startOfMonth(d) {
    return new Date(d.getFullYear(), d.getMonth(), 1);
  }

  function addMonths(d, n) {
    return new Date(d.getFullYear(), d.getMonth() + n, 1);
  }

  var MONTHS = ["January", "February", "March", "April", "May", "June",
    "July", "August", "September", "October", "November", "December"];

  function fmtLabel(d) {
    if (!d) return "—";
    return MONTHS[d.getMonth()].slice(0, 3) + " " + d.getDate() + ", " + d.getFullYear();
  }

  function init(root) {
    var triggerBtn = root.querySelector(".drp-trigger");
    var popover    = root.querySelector(".drp-popover");
    var label      = root.querySelector("[data-drp-label]");
    var title      = root.querySelector("[data-drp-title]");
    var grid       = root.querySelector("[data-drp-grid]");
    var statusEl   = root.querySelector("[data-drp-status]");
    var prevBtn    = root.querySelector('[data-drp-cal="prev"]');
    var nextBtn    = root.querySelector('[data-drp-cal="next"]');
    var resetBtn   = root.querySelector("[data-drp-reset]");

    var minDate = parseISO(root.dataset.min);
    var maxDate = parseISO(root.dataset.max);
    var from    = parseISO(root.dataset.from);
    var to      = parseISO(root.dataset.to);

    // viewMonth is the month currently visible in the calendar. Start it
    // anchored on the existing `to` so the user lands on their current
    // window instead of an arbitrary first month.
    var viewMonth = startOfMonth(to || new Date());

    // pickState: which endpoint the next click sets.
    //   "start" — next click resets the range, setting only start.
    //   "end"   — next click sets end (or swaps if before start) and applies.
    var pickState = "start";

    function updateTriggerLabel() {
      if (from && to) {
        label.textContent = fmtLabel(from) + " – " + fmtLabel(to);
      } else if (from) {
        label.textContent = fmtLabel(from) + " – …";
      } else {
        label.textContent = "Pick a date range";
      }
    }

    function updateStatus() {
      if (pickState === "start") {
        statusEl.textContent = "Pick a start date";
      } else {
        statusEl.textContent = "Pick an end date";
      }
    }

    // Reset is only meaningful when the current view isn't already the
    // trailing-7-day default. Compute the default window from "today" the
    // same way the server's parseDateRange does (to=today, from=today−6)
    // and compare against the dates we were rendered with.
    function defaultWindow() {
      var today = new Date();
      today.setHours(0, 0, 0, 0);
      var from = new Date(today);
      from.setDate(from.getDate() - 6);
      return { from: from, to: today };
    }

    function updateResetEnabled() {
      var def = defaultWindow();
      var atDefault = sameDay(from, def.from) && sameDay(to, def.to);
      // Enabled when the current window differs from the default, or
      // when the user has a half-finished pick (start selected, end not)
      // and might want to bail.
      resetBtn.disabled = atDefault && pickState === "start";
    }

    function renderCalendar() {
      title.textContent = MONTHS[viewMonth.getMonth()] + " " + viewMonth.getFullYear();

      // Disable prev/next at the data boundary so users can't aimlessly
      // scroll into months with nothing to show.
      var prevMonth = addMonths(viewMonth, -1);
      var nextMonth = addMonths(viewMonth, 1);
      prevBtn.disabled = minDate && prevMonth < startOfMonth(minDate);
      nextBtn.disabled = maxDate && nextMonth > startOfMonth(maxDate);

      var year  = viewMonth.getFullYear();
      var month = viewMonth.getMonth();
      var firstDow = new Date(year, month, 1).getDay();
      var daysInMonth = new Date(year, month + 1, 0).getDate();

      var html = "";

      // Leading blanks so the 1st lines up under its weekday header.
      for (var b = 0; b < firstDow; b++) {
        html += '<span class="drp-cell drp-cell-blank" aria-hidden="true"></span>';
      }

      for (var d = 1; d <= daysInMonth; d++) {
        var cell = new Date(year, month, d);
        var iso  = fmtISO(cell);
        var classes = ["drp-cell"];
        var disabled = (minDate && cell < minDate) || (maxDate && cell > maxDate);
        if (disabled) classes.push("drp-cell-disabled");
        if (sameDay(cell, from)) classes.push("drp-cell-start");
        if (sameDay(cell, to))   classes.push("drp-cell-end");
        if (from && to && cell > from && cell < to) classes.push("drp-cell-mid");
        if (sameDay(cell, new Date())) classes.push("drp-cell-today");
        html += '<button type="button" class="' + classes.join(" ") +
          '" data-drp-date="' + iso + '"' +
          (disabled ? " disabled" : "") +
          ' aria-label="' + cell.toDateString() + '">' + d + "</button>";
      }
      grid.innerHTML = html;
    }

    function setOpen(open) {
      triggerBtn.setAttribute("aria-expanded", open ? "true" : "false");
      popover.hidden = !open;
      if (open) {
        // Re-anchor view on `to` each time the popover opens so the user
        // sees their current selection rather than where they last paged.
        viewMonth = startOfMonth(to || from || new Date());
        renderCalendar();
        updateStatus();
      }
    }

    function navigate(newFrom, newTo) {
      var u = new URL(window.location.href);
      u.searchParams.set("from", fmtISO(newFrom));
      u.searchParams.set("to",   fmtISO(newTo));
      u.searchParams.delete("page");
      window.location.assign(u.toString());
    }

    function onCellClick(iso) {
      var picked = parseISO(iso);
      if (!picked) return;

      if (pickState === "start") {
        from = picked;
        to = null;
        pickState = "end";
        updateTriggerLabel();
        updateStatus();
        updateResetEnabled();
        renderCalendar();
        return;
      }

      // pickState === "end": complete the range.
      var newFrom = from;
      var newTo   = picked;
      if (newTo < newFrom) {
        // Click was earlier than start — treat as a corrected start.
        newFrom = picked;
        newTo   = from;
      }
      // Same-day picks make a 1-day range, fine.
      navigate(newFrom, newTo);
    }

    triggerBtn.addEventListener("click", function (e) {
      e.stopPropagation();
      setOpen(popover.hidden);
    });

    prevBtn.addEventListener("click", function () {
      viewMonth = addMonths(viewMonth, -1);
      renderCalendar();
    });
    nextBtn.addEventListener("click", function () {
      viewMonth = addMonths(viewMonth, 1);
      renderCalendar();
    });

    resetBtn.addEventListener("click", function () {
      // Reset = trailing 7 days from today (the server's default when
      // ?from / ?to are omitted). Drop the params and let the handler
      // recompute, then navigate so the page rebuilds with the default
      // range. The popover dismisses naturally because the page reloads.
      var u = new URL(window.location.href);
      u.searchParams.delete("from");
      u.searchParams.delete("to");
      u.searchParams.delete("page");
      window.location.assign(u.toString());
    });

    grid.addEventListener("click", function (e) {
      var btn = e.target.closest("[data-drp-date]");
      if (!btn || btn.disabled) return;
      // renderCalendar replaces grid.innerHTML, which detaches `btn` from
      // the DOM before the click bubbles up. Stop propagation here so the
      // document-level outside-click listener doesn't then see a target
      // that's no longer inside root and close the popover.
      e.stopPropagation();
      onCellClick(btn.getAttribute("data-drp-date"));
    });

    // Dismiss on outside click + Esc.
    document.addEventListener("click", function (e) {
      if (popover.hidden) return;
      if (root.contains(e.target)) return;
      setOpen(false);
    });
    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape" && !popover.hidden) {
        setOpen(false);
        triggerBtn.focus();
      }
    });

    updateTriggerLabel();
    updateResetEnabled();
  }

  document.addEventListener("DOMContentLoaded", function () {
    var nodes = document.querySelectorAll(".drp-nav");
    for (var i = 0; i < nodes.length; i++) init(nodes[i]);
  });
})();
