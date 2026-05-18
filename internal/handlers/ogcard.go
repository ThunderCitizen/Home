package handlers

import "net/http"

// ogCardHTML is the social-share card rendered at exactly 1200×630.
// It is self-contained on purpose: no external CSS/JS/font/image
// requests, so the headless screenshotter (cmd/ogshot) gets a fully
// painted frame the instant the document loads. Colors mirror the
// green-phosphor dark terminal theme (see _mixins.scss dark-theme).
const ogCardHTML = `<!DOCTYPE html>
<html lang="en"><head><meta charset="UTF-8"><style>
*{margin:0;padding:0;box-sizing:border-box}
html,body{width:1200px;height:630px;overflow:hidden}
body{
  background:#0a100a;
  font-family:ui-monospace,"SF Mono",SFMono-Regular,Menlo,Consolas,monospace;
  color:#4ade80;
  display:flex;flex-direction:column;
  padding:72px 88px;
  position:relative;
}
.scan{position:absolute;inset:0;pointer-events:none;
  background:repeating-linear-gradient(0deg,rgba(0,0,0,.10) 0 1px,transparent 1px 4px)}
.glow{position:absolute;inset:0;pointer-events:none;
  box-shadow:inset 0 0 220px rgba(74,222,128,.10)}
.brand{display:flex;align-items:center;gap:22px}
.brand svg{width:60px;height:60px;
  filter:drop-shadow(0 0 6px rgba(74,222,128,.5))}
.brand .wm{font-size:34px;font-weight:700;letter-spacing:.18em;
  color:#86efac;text-shadow:0 0 8px rgba(74,222,128,.4)}
.body{flex:1;display:flex;flex-direction:column;justify-content:center}
.headline{font-size:74px;line-height:1.08;font-weight:700;
  color:#86efac;text-shadow:0 0 10px rgba(74,222,128,.35)}
.tag{margin-top:28px;font-size:32px;color:#22c55e;max-width:920px;line-height:1.4}
.foot{display:flex;align-items:center;justify-content:space-between;
  font-size:24px;color:#1a7a3a}
.foot .cursor{color:#4ade80}
</style></head><body>
<div class="scan"></div><div class="glow"></div>
<div class="brand">
  <svg viewBox="0 0 32 32"><path d="M19 2L7 16h7l-4 14L26 14h-8z" fill="#4ade80" stroke="#4ade80" stroke-width="0.5" stroke-linejoin="round"/></svg>
  <span class="wm">THUNDER CITIZEN</span>
</div>
<div class="body">
  <div class="headline">Thunder Bay's civic<br>data, in one place.</div>
  <div class="tag">Transit tracking, council votes, the budget &amp; more &mdash; open, sourced, no tracking.</div>
</div>
<div class="foot">
  <span>thundercitizen.ca</span>
  <span class="cursor">&gt;_</span>
</div>
</body></html>`

// OGCard serves the static social-share card. Consumed by cmd/ogshot,
// which screenshots it into static/og/*.png. Harmless to leave routed
// in production (it's just a styled page), but it has no inbound links.
func (h *Handlers) OGCard(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(ogCardHTML))
}
