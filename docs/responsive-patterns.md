# Responsive Patterns

## Sticky Table Headers

Tables with data that scrolls beyond the viewport use sticky headers with a glass-effect bar (`backdrop-filter: blur`, `--glass-bg`, `--glass-border`). Two patterns depending on whether the table is inside a horizontal scroll container:

### Pattern 1: CSS-only (no overflow container)

For tables not inside `overflow-x: auto`, sticky works directly on `<thead>` or `<th>`:

```scss
thead {
  position: sticky;
  top: 2.1rem; // clear sticky nav
  z-index: 2;
}
th {
  background: var(--glass-bg);
  backdrop-filter: blur(6px);
  border-bottom: 1px solid var(--glass-border);
}
```

Requires `border-collapse: separate; border-spacing: 0` on the table. Parent containers must not have `overflow: hidden` (use `overflow: clip` instead if clipping is needed for scanlines/rounded corners).

**Used by:** Route directory table (`.route-table` in `transit.templ`)

### Pattern 2: Extracted header + JS sync (inside overflow container)

When the table is inside `overflow-x: auto` (for horizontal scrolling), `position: sticky` can't reach the viewport. Extract the header into a separate sticky element above the scroll container:

1. **Template**: Render header twice via a shared sub-template — once in a `.sticky-header` div above the scroll container, once as a hidden `<thead>` inside the table (for column sizing + a11y)
2. **CSS**: `.sticky-header` gets `position: sticky; top: 2.1rem` + glass effect. Original `<thead>` gets `visibility: collapse`
3. **JS**: Sync column widths from the hidden thead to the clone, and sync `scrollLeft` on the scroll container's `scroll` event. Re-run on `htmx:afterSwap` for dynamic content

**Used by:** Route timetable (`.route-tp-sticky-header` in `route.templ`), vote matrix photo bar (`.vote-matrix-photo-bar` in `councillors.templ`)

### Gotchas
- `overflow: hidden` on any ancestor kills sticky — switch to `overflow: clip`
- `overflow-x: auto` implicitly sets `overflow-y: auto`; add `overflow-y: hidden` if vertical scroll is unwanted
- The article scanline rule (`article > *:not(.sr-only)`) sets `position: relative` on direct children — exclude sticky elements via `:not(.your-sticky-class)`
- `top: 2.1rem` assumes the site's sticky nav height; adjust if nav changes

## Responsive Toolbar Pattern (`.terminal-map-header`)

The transit map header packs four logical chunks (title, live status, layers group, features group) into a single bar that **must stay readable as it shrinks**, then **wrap into an aligned 2-row grid** (not a flex-wrap scramble).

### Structure

DOM is flat — no sub-wrappers around title/status. Each button group is `.selector-bar` containing a `.selector-bar-label` and a `.selector-bar-btns` (the inner div is required so subgrid/contents collapse can target the buttons as a unit):

```html
<div class="terminal-map-header">
  <span class="terminal-map-title">…</span>
  <span id="transit-status">…</span>
  <div id="selector-bar"><div class="selector-bar"><span class="selector-bar-label">Layers</span><div class="selector-bar-btns">…</div></div></div>
  <div class="selector-bar"><span class="selector-bar-label">Features</span><div class="selector-bar-btns">…</div></div>
</div>
```

### Layout (CSS grid, two modes)

**Wide (≥1024px)** — single row, four cols:

```scss
grid-template-columns: auto auto 1fr auto;
grid-template-areas: "title status layers features";
> #selector-bar { justify-self: center; }   // layers floats in the 1fr middle
> .selector-bar:last-child { justify-self: end; }
```

**lg-down (≤1024px)** — two rows, label/btns columns shared so labels line up vertically:

```scss
grid-template-columns: auto 1fr auto auto;
grid-template-areas:
  "title  .  ftr-label ftr-btns"
  "status .  lyr-label lyr-btns";
column-gap: 0.4rem;

> .selector-bar:last-child, > #selector-bar, > #selector-bar > .selector-bar { display: contents; }
.selector-bar-label, .selector-bar-btns { /* assigned to ftr-* / lyr-* areas */ }
```

`display: contents` collapses the wrappers so their inner label + btns become direct grid children of `.terminal-map-header` and snap to the shared columns. Without this the labels won't align (each wrapper would resolve its own column inside its own track).

### Rules

- **Don't use flex-wrap on toolbars with multiple labeled groups** — wrap order is unpredictable and labels won't align across rows. Use grid with named areas + `display: contents` on group wrappers.
- **`white-space: nowrap` on `.terminal-map-title` and `.selector-bar-btn`** — without it, narrow viewports break label text inside its own button.
- **Status (`• Live`) is exempt from column alignment** — it sits in col 1 row 2 (lg-down) or col 2 row 1 (wide), never gets pinned to a label/btn line. By design.
- Wrap threshold is `respond-to('lg')` (1024px). Below that, the 2-row grid is roomy enough that section labels (LAYERS / FEATURES) stay visible.

## Table ↔ Card Swap (data-row tables)

When a table's rows are **independent data items** (cancellations, votes, motions, audit lines) — not a pivot/matrix — narrow viewports are better served by stacked cards than by hidden columns or horizontal scroll. The pattern: render two parallel DOMs and let CSS swap them at a breakpoint.

### When to use

- Each row stands alone (a single cancelled trip, a single motion).
- You have ≥4 columns and at least one is long-form text (headsign, motion title).
- Hiding columns would lose information riders actually want on mobile.

**Don't** use this on dense numeric tables where the comparison *across* rows is the point (timetables, KPI matrices) — there, horizontal scroll with a sticky header (Pattern 2 above) is better.

### Mechanics

1. **Render both structures.** Server-rendered: emit `.foo-table` and `.foo-card-list` sibling elements. Client-rendered (JS): build both inside `renderTable()` so re-sorts repaint both in lock-step.
2. **CSS swaps via `display`:**
   ```scss
   .foo-card-list { display: none; }
   @include respond-to('md') {
     .foo-table     { display: none; }
     .foo-card-list { display: flex; flex-direction: column; gap: var(--space-2); }
   }
   ```
3. **Default breakpoint:** `respond-to('md')` (768px). Use a wider threshold only when the table has many columns (vote matrix uses 1199px).
4. **Card shape — labeled-cell hybrid.** Each card has two zones:
   - **Tinted header** (`background: var(--term-bg-deep)`, 1px bottom border, no top corners since the card's left-border accent runs full-height): identity badge (colored pill, ~1.75rem square) + primary label (headsign / motion title, ellipsizes). One row, grid `auto 1fr`.
   - **Field row**: a flex row of equal-width cells, each `<div class="foo-field"><dd>value</dd><dt>LABEL</dt></div>` with 1px vertical `<span class="foo-field-sep">` dividers between cells. Value sits over an uppercase 0.55rem micro-label (same pattern as the live-map `.route-pill-footer`). Status (e.g. "19m before") is just another cell whose `dd` is colored `--status-error` / `--status-ok`; the `dt` stays neutral.
   - Left-border accent on the card (`border-left: 4px solid var(--status-*)`) encodes good/bad at a glance — don't put a badge in the header for the same signal.
   - Don't add an extra "body" zone for prose between header and fields. If you need long text (headsign), put it in the header next to the badge; the field row is for atomic key/value pairs only.
5. **Section dividers** (e.g. date groups) appear in both layouts — render a `.foo-card-divider` on the card side that mirrors the table's `.foo-td-date-header` row.
6. **Sort:** keep sort controls on the table only. Mobile cards inherit the last-applied sort order. Sorting from a phone is rare; reserving headers for the table avoids duplicate controls.

### Wide-screen companion: flex the long column

Independent of the swap, a fixed `max-width` on the long-text column (e.g. `max-width: 20rem`) leaves dead space on big monitors. Let it absorb slack while still ellipsizing on overflow:

```scss
.foo-td-headsign {
  width: 100%;
  max-width: 0;      // lets ellipsis kick in only when content actually overflows
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
```

All other cells stay `white-space: nowrap` and size-to-content; the table distributes remaining width to the `width: 100%` column.

### Implementations to copy

- **Vote matrix** — `static/css/style.scss` `.vote-matrix-mobile` (~line 1402); templates in `templates/pages/councillors.templ`. Uses a 1200px threshold and card pagination (`.vote-matrix-show-all`).
- **Cancel log** — `static/css/_transit.scss` `.cl-card-list` block; JS in `static/transit/trends-chart.js` `renderCancelLog`. Uses `md` (768px) and re-renders cards on every sort.

### Anti-pattern

Don't try to `display: block` the `<tr>` and reflow `<td>`s into pseudo-cards via CSS only. It fights row striping, kills sticky headers, breaks screen-reader table semantics, and the result is fragile when content lengths change. Render two DOMs.
