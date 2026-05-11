# Accessibility

Thunder Citizen aims for **WCAG 2.2 AA** compliance, with select AAA criteria where they serve our civic mission. Public data should be accessible to everyone.

Reference: [WCAG 2.2 Quick Reference](https://www.w3.org/WAI/WCAG22/quickref/)

## Our Standards

| Level | Target | Notes |
|-------|--------|-------|
| A | **Required** | All criteria must pass |
| AA | **Required** | All criteria must pass |
| AAA | **Selective** | Adopted where practical (see below) |

## Compliance by Principle

### 1. Perceivable

| Criterion | Level | Status | How We Meet It |
|-----------|-------|--------|----------------|
| 1.1.1 Non-text Content | A | ✅ | Maps have `role="img"` + `aria-label`. Budget bars have sr-only data table. Avatars `aria-hidden`. |
| 1.3.1 Info and Relationships | A | ✅ | Semantic HTML: `<nav>`, `<main>`, `<article>`, `<table>` with `<caption>` and `scope`. Headings h1-h2 hierarchy. |
| 1.3.2 Meaningful Sequence | A | ✅ | DOM order matches visual order. No CSS reordering that breaks reading flow. |
| 1.3.3 Sensory Characteristics | A | ✅ | Budget data in both visual bars AND data table. No "click the blue button" instructions. |
| 1.4.1 Use of Color | A | ✅ | Budget bars have labels + percentages alongside color. Route colors paired with route numbers. |
| 1.3.4 Orientation | AA | ✅ | Responsive layout works in both orientations. |
| 1.4.3 Contrast (Minimum) | AA | ✅ | All text colors use `--thunder-600` (#486581) or darker on light backgrounds — 5.5:1 minimum. Badge uses dark text on light amber — 6.4:1. |
| 1.4.4 Resize Text | AA | ✅ | Relative units (rem). Tested to 200% zoom. |
| 1.4.5 Images of Text | AA | ✅ | No images of text. All text is real text. |
| 1.4.10 Reflow | AA | ✅ | Single-column at 320px. Tables scroll horizontally. |
| 1.4.11 Non-text Contrast | AA | ✅ | Budget bar fills are bold colors (blue, orange, red, green) against light gray track — all exceed 3:1. Focus outlines are 3px accent blue. |
| 1.4.12 Text Spacing | AA | ✅ | No fixed heights on text containers. Pico handles gracefully. |

### 2. Operable

| Criterion | Level | Status | How We Meet It |
|-----------|-------|--------|----------------|
| 2.1.1 Keyboard | A | ⚠️ | Accordions: native `<details>` = keyboard accessible. Maps: Leaflet provides basic keyboard zoom but marker interaction is mouse-only. |
| 2.1.2 No Keyboard Trap | A | ✅ | No modal dialogs or focus traps. Tab moves through all elements. |
| 2.1.4 Character Key Shortcuts | A | ✅ | No single-character shortcuts defined. |
| 2.2.1 Timing Adjustable | AA | ✅ | No time limits. Transit data polls indefinitely. |
| 2.2.2 Pause, Stop, Hide | AA | ✅ | Live stats update text in-place (no movement). Map markers reposition without animation. |
| 2.3.1 Three Flashes | AA | ✅ | No flashing content. |
| 2.4.1 Bypass Blocks | AA | ✅ | Skip-to-main link. Landmark regions (`<nav>`, `<main>`, `<footer>`). |
| 2.4.2 Page Titled | AA | ✅ | Each page: `{Page} - Thunder Citizen`. |
| 2.4.3 Focus Order | AA | ✅ | DOM order = tab order. No `tabindex` > 0. |
| 2.4.4 Link Purpose | AA | ✅ | Links have descriptive text. External links marked with `rel="noopener"`. |
| 2.4.7 Focus Visible | AA | ✅ | `*:focus-visible` with 3px accent outline. Nav toggle gets white ring on dark header. |
| 2.4.11 Focus Not Obscured | AA | ✅ | No sticky overlays or modals that could obscure focus. |
| 2.5.1 Pointer Gestures | AA | ✅ | No multi-finger or path-dependent gestures. Map zoom via buttons. |
| 2.5.2 Pointer Cancellation | AA | ✅ | Standard click events (activate on up). |
| 2.5.3 Label in Name | AA | ✅ | Visible labels match accessible names. |

### 3. Understandable

| Criterion | Level | Status | How We Meet It |
|-----------|-------|--------|----------------|
| 3.1.1 Language of Page | A | ✅ | `<html lang="en">` |
| 3.2.1 On Focus | A | ✅ | No context changes on focus. |
| 3.2.2 On Input | A | ✅ | No forms that auto-submit. |
| 3.2.3 Consistent Navigation | AA | ✅ | Same nav bar on every page, same order. |
| 3.2.4 Consistent Identification | AA | ✅ | Same components (cards, stats, accordions) used consistently. |

### 4. Robust

| Criterion | Level | Status | How We Meet It |
|-----------|-------|--------|----------------|
| 4.1.2 Name, Role, Value | A | ✅ | ARIA roles on maps, live regions, nav. Native HTML elements for interactive controls. |
| 4.1.3 Status Messages | A | ✅ | Transit status: `role="status"` + `aria-live="polite"`. Alpha banner: `role="status"`. Stats: `aria-live="polite"` + `aria-atomic="true"`, only fires on value change. |

## AAA Criteria We Adopt

These aren't required for AA but serve our civic transparency mission:

| Criterion | Why |
|-----------|-----|
| 2.4.5 Multiple Ways | Nav links + home page quick links to all sections |
| 2.4.6 Headings and Labels | Descriptive headings on every section — budget data must be scannable |
| 2.4.10 Section Headings | All content organized under clear h2 sections |
| 1.3.6 Identify Purpose | Landmark regions labeled (`aria-label` on nav, map, stats region) |
| 2.4.8 Location | `aria-current="page"` on active nav link |

## Known Gaps

### Map keyboard interaction (2.1.1 partial)

Leaflet maps support keyboard zoom but individual markers aren't keyboard-focusable. Mitigation:

- Ward map: `aria-label` directs users to the councillor list below, which has all the same information in accessible accordions
- Transit map: `aria-label` describes what the map shows. Live stats below provide the key data (bus count, route count) without needing the map

**Future**: Add a text-based route/vehicle list view as an alternative to the map.

### Color contrast audit (1.4.11)

Budget bar fill colors need 3:1 against the track background (`--thunder-100` #d9e2ec). Most pass but verify when adding new colors. Run the contrast check in the testing checklist.

## Implementation Patterns

### Landmarks (native HTML)

Use semantic HTML; **do not** add redundant ARIA roles. Every native element below has an implicit role that screen readers already recognize:

| Element                | Implicit role  | Use for                              |
| ---------------------- | -------------- | ------------------------------------ |
| `<header>` (top level) | `banner`       | Site header in `layout.templ`        |
| `<nav>`                | `navigation`   | Any nav block (always `aria-label` it if there are multiple) |
| `<main>`               | `main`         | Page content wrapper (one per page)  |
| `<footer>` (top level) | `contentinfo`  | Site footer in `layout.templ`        |
| `<section aria-label>` | `region`       | Major sub-area of a page             |

Writing `role="banner"` on a `<header>` is redundant noise. Only add explicit roles when no native element fits (e.g., `role="tablist"`, `role="radiogroup"`, `role="status"`).

### Heading hierarchy

**Every page must have exactly one `<h1>`.** It names the page for screen-reader users who jump by heading. Either render a visible `<h1>` (preferred when the page has a clear title) or an `<h1 class="sr-only">` when the visual design already conveys the page name through terminal-styled chrome.

- Pages built on `@components.PageHeader` or `@components.Hero` inherit an h1 from the component.
- Pages using `@components.HeroStats` or `@components.TermPanel` do **not** get an h1 from those components — they render their titles as `<h3>`. Add an `<h1 class="sr-only">` above the TermPanel.
- Inside a page, step heading levels by one (`h1 → h2 → h3`). Avoid jumping `h1 → h3`. Avoid inversions (`h3 → h2`).

### Data visualizations

Always provide **two representations**:

1. Visual (charts, bars, colors) — marked `aria-hidden="true"`
2. Tabular (sr-only `<table>` with `<caption>`) — full data accessible to screen readers

Example: budget bars have a hidden table with Service, Amount, Share, and Change columns.

### Live-updating content

- Use `aria-live="polite"` — never `"assertive"` for periodic data
- Set `aria-atomic="true"` so the full value is announced, not a diff
- Only update the DOM when values actually change (avoids repeated announcements)
- Status changes (connecting/live/error) use `role="status"`

### Decorative elements

Mark with `aria-hidden="true"`:
- Color swatches (the data is in adjacent text)
- Avatar initials (the name is in the adjacent `<strong>`)
- Chart fills (the data is in the sr-only table)

### Reduced motion

All animations and transitions are disabled via `@media (prefers-reduced-motion: reduce)`.

### Print

Navigation, maps, footer, and decorative elements are hidden. External link URLs are shown inline.

## Testing

### Level 1: Manual browser audit

For checks that require a live page (color contrast, nested interactives, Leaflet-generated DOM), use the [axe DevTools](https://www.deque.com/axe/devtools/) browser extension against the running dev server. Run WCAG 2.2 AA rules on each page.

### Level 2: Manual testing checklist

Before each release:

- [ ] **Keyboard**: Tab through every page — can you reach and operate everything?
- [ ] **Screen reader**: VoiceOver (Cmd+F5 on Mac) — do all pages make sense read aloud?
- [ ] **Zoom**: Browser zoom to 200% — does anything overflow or get clipped?
- [ ] **Reflow**: Resize browser to 320px wide — single column, no horizontal scroll?
- [ ] **Reduced motion**: Set `prefers-reduced-motion: reduce` in system preferences — no animations?
- [ ] **Print**: Print preview (Cmd+P) — readable without nav/map clutter?
- [ ] **Color contrast**: Check any new colors against [WebAIM Contrast Checker](https://webaim.org/resources/contrastchecker/)
- [ ] **Content**: Do headings describe their sections? Are link texts descriptive?
