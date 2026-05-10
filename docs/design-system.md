# Design System

Terminal aesthetic site-wide. Solarized cream light mode + green phosphor dark mode. All theme colors via CSS custom properties — never hardcode hex for theme decisions. Use `color-mix()` for tinted backgrounds (badges, alerts, motion cards).

## Palettes

**Light (Fractured Stone — cool/ash):** three-tier stone hierarchy — page `#e1d9c9` (light stone), card body `#cfc7b5` (mid stone), nav/strip/footer `#beb5a1` (deep stone). Solarized blue `#155a8a` for interactive affordances (links, buttons, focus) and deeper `#0b4670` for headings/terminal labels — both darkened from prior cream-palette values so AA holds on the deeper surfaces. Muted text (`--term-fg-dim`) is `#2f4550` — darkened so it reaches 4.9:1 on the deep-stone strip (footer, card footer, panel bars), not just the mid-stone card body.

**Dark (Green Phosphor):** near-black green bg `#0d1a0d` (page), `#141e14` (card, lifted above page per Material-Dark elevation convention), `#0a100a` (strip/nav/footer, deepest). Phosphor green text `#4ade80` (10.3:1). Nav + site footer carry a phosphor-green halo box-shadow (`--strip-edge-glow-below/-above`) so they frame visibly against the near-black tiers. Headings collapse onto `--accent` — single-phosphor CRT vibe. CRT scanlines on header, green glow on title.

Dark mode is defined via `@mixin dark-theme` applied to both `@media (prefers-color-scheme: dark)` and `:root[data-theme="dark"]`. Console helper: `toggleTheme()` / `toggleTheme("dark")` / `toggleTheme("light")`.

## CSS variables (`:root` in `static/css/_tokens.scss`)

| Variable | Light | Dark | Purpose |
|----------|-------|------|---------|
| `--thunder-900` to `--thunder-50` | Solarized grey scale | Green phosphor scale | Text/bg hierarchy (flips) |
| `--accent` | `#4a6100` (olive green) | `#4ade80` (phosphor) | Interactive affordances: links, buttons, focus rings |
| `--heading` | `#155a8a` (Solarized blue, darkened) | `var(--accent)` — phosphor green | h1/h2/h3 + terminal-label family. Light uses classic Solarized blue; dark collapses onto --accent so the green-phosphor CRT look stays unified |
| `--heading-glow` | `none` | phosphor bloom | Heading text-shadow. Off in light; `0 0 6px rgba(74,222,128,0.35)` in dark for the CRT glow |
| `--heading-warm` | `#7a3f0a` (darkened Solarized orange) | `var(--accent)` — phosphor green | Scoped warm-accent variant. Applied via `.home article > header` to give home-page card header bars a rust/amber tone against the beige strip. Dark mode collapses onto --accent so the phosphor look stays unified |
| `--term-*` tokens | Solarized values | Phosphor values | Semantic terminal tokens (bg, fg, border, glow) |
| `--surface-dark` | `#002b36` | `#0a100a` | Header/footer background |
| `--status-ok/warn/error/info/early-dep/muted` | Darkened for AA on cream | Bright for dark bg | Status semantics |
| `--proposal-1/2/3` | Stable | Stable | Proposal accent colors |

## Typography

**Mono-only.** There is a single type face site-wide — `--font-mono`. `--font-prose` is an alias that resolves to `--font-mono`, so existing `font-family: var(--font-prose)` declarations (e.g. `.lead`, `.motion-text`, `.motion-heading`, `.motion-agenda-item`, `.sankey-detail-body p`, `.councillor-bio`) continue to work but render in mono. Don't add new `--font-prose` consumers; new code should inherit the default mono or omit `font-family` entirely.

**Mono stack** covers Apple (SF Mono, Menlo) → Windows 11 (Cascadia Mono) → Windows (Consolas) → Android (Roboto Mono) → Ubuntu (Ubuntu Mono) → other Linux (DejaVu Sans Mono) → older Windows (Courier New) → generic `monospace`. Full stack in `static/css/_tokens.scss`.

**Headings** are terminal labels: all `0.72rem`, uppercase, `letter-spacing: 0.08em`, `color: var(--heading)` (Solarized blue in light, phosphor green in dark). Weight is the hierarchy lever: h1=800, h2=700, h3=600. In dark mode a subtle phosphor text-shadow is applied via `--heading-glow` (no-op in light). `--accent` is reserved for interactive affordances (links, buttons, focus) — never use it for heading text.

## Accessing theme colors

- **SCSS**: `color: var(--status-error);` or `background: color-mix(in srgb, var(--status-error) 15%, var(--thunder-50));`
- **Templates**: Use `.text-status-ok`, `.text-status-error` etc. utility classes. For inline styles: `style="border-left-color:var(--proposal-1)"`.
- **Vanilla JS**: `var tc = ThemeColors();` then `tc.statusOk`, `tc.accent`, `tc.termAccent`, etc. (`static/js/theme-colors.js` loaded globally)
- **TypeScript**: `import { readThemeColors } from "../theme-colors";` then call after DOMContentLoaded

## Card system

Two reusable card placeholders — pick by chrome weight, not by class name:

| Placeholder | Defined in | Chrome | Use for |
|-------------|-----------|--------|---------|
| `%card-accent` | `_placeholders.scss` | thin border, no shadow, `--space-3` padding | One row in a list of many (directory items, proposal cards) |
| `%card-base`   | `_placeholders.scss` | bordered, `--card-shadow`, `--space-4` padding | Standalone summary unit (motion card, terminal card, summary panels) |

Variant tinting (left-border + bg-tint, e.g. carried/lost/voted): use `motion-card-variant($base)` in `_mixins.scss` — pass the surface color the card sits on so the tint mix lands on the right base.

Don't reach for either on **list rows** or **table cells** — the term "card" is overloaded and a row/cell needs neither shadow nor padding-4.

## Spacing & type scale enforcement

Use tokens, not literals:

- Spacing: `var(--space-1..8)` (4–48px) — `_tokens.scss:114`
- Type:    `var(--text-2xs..2xl)` (0.6–2rem) — `_tokens.scss:134`
- Weight:  `var(--weight-normal/medium/semi/bold/heavy)` (400–800) — `_tokens.scss:145`
- Radius:  `var(--radius-sm/md/lg/full)` — `_tokens.scss:152`
- Contrast: `var(--text-on-dark)` for white/cream text on dark surfaces — `_tokens.scss:164`

When adding new CSS: snap to the nearest token. If no token fits within ±0.03rem, the literal is probably wrong — re-pick the size from the scale rather than inventing a new token. Hardcoded `#fff`, `4px` border-radii, `0.65rem` font-sizes, or `font-weight: 700` magic numbers will be rejected during review. Outliers that intentionally sit off-scale (full-pill `999px`, hairline `2px`, light-300/black-900 weights, 0-resets) keep literals; everything else snaps.

## Header pattern matrix

There are several heading/hero containers — pick by use case, don't invent new ones:

| Use case | Use |
|----------|-----|
| Page-level hero (data-led, dark surface + scanlines) | `.hero` |
| Home-page hero (intro/lead, terminal label) | `.home-hero` |
| Standard page title bar | `.page-header` |
| Map overlay title bar | `terminal_map_header.templ` component |
| Article card title | `<article><header>` (Pico default, styled in `style.scss`) |

## Phosphor Pills

All colored pill/badge/label elements use the **phosphor pill system** — two Sass mixins that emit `--badge-*` CSS tokens consumed by `%badge-base` (in `_placeholders.scss`).

**Light mode** (`badge-light-hue($hue)`) — ward-map aesthetic: washed tinted fill (22% hue on cream), solid hue border, darkened hue text, no glow.

**Dark mode** (`badge-dark-hue($hue)`) — CRT aesthetic: dark near-black fill (14% hue tint), saturated phosphor text + border, drop-shadow glow, scanline overlay.

**Size system** — the pill's size signals whether it's clickable. Pick one:

| Placeholder | Size | Use for | Class name pattern |
|-------------|------|---------|--------------------|
| `%badge-info` | 0.55rem | Static labels (status, tags, counts) | Nouns: `-status`, `-badge`, `-tag`, `-label`, `-count` |
| `%badge-action` | 0.72rem | Interactive `<a>` / `<button>` only | Verbs: `-btn`, `-action`, `-link` |

Keep the contract — bigger pills are clickable, smaller pills are not. Mismatched size ↔ interactivity is a bug: fix the class name, not the styles.

**Theme rules use a symmetric mixin pattern.** Light and dark hues for `.badge-X` modifiers (carried/lost/term-*, ward selectors, etc.) live in mirrored aggregator mixins in `style.scss`:

```scss
@mixin badge-light-overrides { .badge { ... }  @include council.badge-light-overrides-council; ... }
@mixin badge-dark-overrides  { .badge { ... }  @include council.badge-dark-overrides-council;  ... }

:root                              { @include badge-light-overrides; }
@media (prefers-color-scheme: dark) {
  :root:not([data-theme])          { @include badge-dark-overrides; }
}
:root[data-theme="dark"]           { @include badge-dark-overrides; }
```

Why the `:root` wrapper for **both** themes (not just dark): bare `.badge { ... }` in `style.scss` is declared after the partials are `@use`'d, so unwrapped modifier selectors (`.badge-term-1`) lose to it on source order. Wrapping at `:root` adds a parent-selector specificity bump and breaks the tie. Dark wins over light because `:root[data-theme="dark"]` has one more attribute selector than `:root`.

**Domain split.** Council variants live in `_council.scss` inside two mirrored mixins: `badge-light-overrides-council` and `badge-dark-overrides-council`. Cross-domain (`.badge` base, `.badge-muted`, `/data` admin pills) lives in the `style.scss` aggregators directly. If you create a new domain-owned family, add `badge-{light,dark}-overrides-<domain>` mixins in your partial and `@include` both from the matching aggregator.

**To add a new pill variant:**
1. Pick the surface (size + interactivity contract): `@extend %badge-base; @extend %badge-info;` (or `%badge-action` for interactive pills) on the component selector. **Don't include a hue here** — hues live in the theme mixins.
2. Add the hue in **both** mixins (same partial, mirrored): `.my-pill { @include badge-light-hue(#hex); }` in light, `@include badge-dark-hue(#hex);` in dark. Forgetting one leaves the pill themeless on that side.

Existing consumers: `.badge-*` (result/significance/term), ward subtitle badges, `.motion-filter-pill--*` (active state), `.recent-meeting-status` (info), `.meeting-row-btn` (action). Full docs in `_mixins.scss`.

## What NOT to tokenize
- **Route identity colors** (ROUTE_COLORS maps) — GTFS data, not theme. Also used for Sankey budget nodes.
- **Ward identity colors** — data
- **Term badge colors** (belt progression) — domain data; hues live in the mirrored `badge-{light,dark}-overrides-council` mixins
- **HSL interpolations** (delay ring gradient) — computed, not a token
