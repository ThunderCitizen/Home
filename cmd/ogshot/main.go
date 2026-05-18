// Command ogshot screenshots the social-share card served at
// /og/render into the static/og/*.png files that layout.templ unfurls.
//
// One card, all pages: the same 1200×630 PNG is written to every
// og filename so every page shares the identical preview. Re-run
// whenever the card design (internal/handlers/ogcard.go) changes.
//
// Usage:
//
//	go run ./cmd/ogshot                 # against http://localhost:8080
//	go run ./cmd/ogshot -base http://localhost:3000
//
// Requires a running server and a local Chrome/Chromium (chromedp
// auto-discovers the system browser).
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/chromedp"
)

// Every page's og:image resolves to one of these (see layout.templ
// fallbackImageForPath + home.templ). Writing the same bytes to all
// of them gives the whole site a single shared preview card.
var targets = []string{
	"default.png", "home.png", "transit.png",
	"councillors.png", "minutes.png", "budget.png",
}

func main() {
	base := flag.String("base", "http://localhost:8080", "running server base URL")
	outDir := flag.String("out", "static/og", "output directory")
	flag.Parse()

	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	ctx, cancel = context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	var png []byte
	err := chromedp.Run(ctx,
		// scale 1 → output is exactly 1200×630, matching the
		// og:image:width/height layout.templ declares.
		chromedp.EmulateViewport(1200, 630),
		chromedp.Navigate(*base+"/og/render"),
		chromedp.WaitVisible(".headline", chromedp.ByQuery),
		chromedp.FullScreenshot(&png, 100),
	)
	if err != nil {
		log.Fatalf("screenshot failed (is the server up at %s?): %v", *base, err)
	}

	for _, name := range targets {
		p := filepath.Join(*outDir, name)
		if err := os.WriteFile(p, png, 0o644); err != nil {
			log.Fatalf("write %s: %v", p, err)
		}
		log.Printf("wrote %s (%d KB)", p, len(png)/1024)
	}
	log.Printf("done — %d files, 1200x630 @2x", len(targets))
}
