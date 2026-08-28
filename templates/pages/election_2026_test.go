package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/net/html"

	"thundercitizen/internal/middleware"
	"thundercitizen/internal/views"
)

func renderElection2026(t *testing.T) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://thundercitizen.ca/election/2026?ward=northwood", nil)
	rr := httptest.NewRecorder()
	h := middleware.Social("https://thundercitizen.ca")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := Election2026(views.NewElection2026ViewModel()).Render(r.Context(), w); err != nil {
			t.Fatalf("render election: %v", err)
		}
	}))
	h.ServeHTTP(rr, req)
	return rr.Body.String()
}

func parseElectionHTML(t *testing.T, body string) *html.Node {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parse rendered HTML: %v", err)
	}
	return doc
}

func nodeAttr(n *html.Node, key string) (string, bool) {
	for _, attr := range n.Attr {
		if attr.Key == key {
			return attr.Val, true
		}
	}
	return "", false
}

func hasClass(n *html.Node, class string) bool {
	classes, _ := nodeAttr(n, "class")
	for _, got := range strings.Fields(classes) {
		if got == class {
			return true
		}
	}
	return false
}

func hasToken(value, token string) bool {
	for _, got := range strings.Fields(value) {
		if got == token {
			return true
		}
	}
	return false
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(cur *html.Node) {
		if cur.Type == html.TextNode {
			b.WriteString(cur.Data)
		}
		for child := cur.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

func walkNodes(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		walkNodes(child, fn)
	}
}

func TestElection2026RenderedStructure(t *testing.T) {
	body := renderElection2026(t)
	doc := parseElectionHTML(t, body)

	h1Count := 0
	candidateCards := 0
	categoryRadios := 0
	wardRadios := 0
	trusteeRadios := 0
	checkedCategories := make([]string, 0)
	checkedSubcontests := make([]string, 0)
	radioControls := make(map[string]string)
	labelFors := make(map[string]bool)
	ids := make(map[string]bool)
	duplicateIDs := make(map[string]bool)
	allCardsHaveHeading := true
	cityProfileLinks := 0
	categoryPanels := 0
	ballotExplorers := 0
	completeBallotExplorers := 0
	mapLayers := make(map[string]int)
	mediaLists := 0
	mediaLabels := 0
	mediaDetails := 0

	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		if n.Data == "h1" {
			h1Count++
		}
		if id, ok := nodeAttr(n, "id"); ok {
			if ids[id] {
				duplicateIDs[id] = true
			}
			ids[id] = true
		}
		if n.Data == "label" {
			if target, ok := nodeAttr(n, "for"); ok {
				labelFors[target] = true
			}
		}
		if n.Data == "input" && hasClass(n, "election-radio-input") {
			id, _ := nodeAttr(n, "id")
			controls, _ := nodeAttr(n, "aria-controls")
			radioControls[id] = controls
			_, checked := nodeAttr(n, "checked")
			switch {
			case hasClass(n, "election-category-input"):
				categoryRadios++
				if checked {
					checkedCategories = append(checkedCategories, id)
				}
			case hasClass(n, "election-subcontest-input-ward"):
				wardRadios++
				if checked {
					checkedSubcontests = append(checkedSubcontests, id)
				}
			case hasClass(n, "election-subcontest-input-trustee"):
				trusteeRadios++
				if checked {
					checkedSubcontests = append(checkedSubcontests, id)
				}
			}
		}
		if n.Data == "article" && hasClass(n, "election-candidate-card") {
			candidateCards++
			hasHeading := false
			walkNodes(n, func(child *html.Node) {
				if child.Type == html.ElementNode && (child.Data == "h3" || child.Data == "h4") {
					hasHeading = true
				}
			})
			allCardsHaveHeading = allCardsHaveHeading && hasHeading
		}
		if n.Data == "div" && hasClass(n, "election-category-panel") {
			categoryPanels++
		}
		if n.Data == "section" && hasClass(n, "election-browser") {
			ballotExplorers++
			hasSelector := false
			hasResults := false
			walkNodes(n, func(child *html.Node) {
				hasSelector = hasSelector || (child.Type == html.ElementNode && hasClass(child, "election-category-panel"))
				hasResults = hasResults || (child.Type == html.ElementNode && hasClass(child, "election-view-panels"))
			})
			if hasSelector && hasResults {
				completeBallotExplorers++
			}
		}
		if n.Data == "a" && nodeText(n) == "City-submitted candidate profiles ↗" {
			cityProfileLinks++
			href, _ := nodeAttr(n, "href")
			if href != views.Election2026CandidateProfilesURL {
				t.Errorf("City profile link has unexpected href %q", href)
			}
		}
		if n.Data == "button" {
			if layer, ok := nodeAttr(n, "data-layer"); ok {
				mapLayers[layer]++
			}
		}
		if n.Data == "section" && hasClass(n, "election-candidate-media") {
			mediaLists++
			walkNodes(n, func(child *html.Node) {
				if child.Type == html.ElementNode && hasClass(child, "election-candidate-media-label") && nodeText(child) == "Links" {
					mediaLabels++
				}
			})
		}
		if n.Data == "details" && hasClass(n, "election-candidate-media") {
			mediaDetails++
		}
	})

	if h1Count != 1 {
		t.Errorf("h1 count = %d, want 1", h1Count)
	}
	if candidateCards != 72 {
		t.Errorf("candidate cards = %d, want 72", candidateCards)
	}
	if cityProfileLinks != 1 {
		t.Errorf("City profile links = %d, want 1", cityProfileLinks)
	}
	if categoryPanels != 1 {
		t.Errorf("category selector panels = %d, want 1", categoryPanels)
	}
	if ballotExplorers != 1 || completeBallotExplorers != 1 {
		t.Errorf("shared ballot explorers = %d complete of %d, want 1 complete of 1", completeBallotExplorers, ballotExplorers)
	}
	if !reflect.DeepEqual(mapLayers, map[string]int{"wards": 1}) {
		t.Errorf("ward map layers = %#v, want one wards toggle", mapLayers)
	}
	if mediaLists == 0 || mediaLabels != mediaLists {
		t.Errorf("Links labels = %d for %d candidate link lists", mediaLabels, mediaLists)
	}
	if mediaDetails != 0 {
		t.Errorf("candidate Links lists use %d disclosure elements, want none", mediaDetails)
	}
	if strings.Contains(body, "Sources (") {
		t.Error("candidate cards must label their source lists Links, not Sources with a count")
	}
	if categoryRadios != 4 {
		t.Errorf("category radios = %d, want 4", categoryRadios)
	}
	if wardRadios != 7 {
		t.Errorf("ward radios = %d, want 7", wardRadios)
	}
	if trusteeRadios != 4 {
		t.Errorf("trustee radios = %d, want 4", trusteeRadios)
	}
	if len(checkedCategories) != 1 || checkedCategories[0] != "election-category-mayor" {
		t.Errorf("checked categories = %#v, want Mayor only", checkedCategories)
	}
	if len(checkedSubcontests) != 0 {
		t.Errorf("an arbitrary ward or board must not be preselected: %#v", checkedSubcontests)
	}
	if len(duplicateIDs) != 0 {
		t.Errorf("duplicate IDs: %#v", duplicateIDs)
	}
	if !allCardsHaveHeading {
		t.Error("every candidate article must have a heading")
	}
	for radioID, panelID := range radioControls {
		if !labelFors[radioID] {
			t.Errorf("radio %q has no matching label", radioID)
		}
		if panelID == "" || !ids[panelID] {
			t.Errorf("radio %q controls missing panel %q", radioID, panelID)
		}
	}
	if strings.Contains(body, " hidden") {
		t.Error("ward candidate content must be present in the initial HTML, not hidden server-side")
	}

	var mainNav *html.Node
	walkNodes(doc, func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "nav" {
			if label, _ := nodeAttr(n, "aria-label"); label == "Main navigation" {
				mainNav = n
			}
		}
	})
	if mainNav == nil {
		t.Error("main navigation is missing")
	} else {
		featurePosition := -1
		position := 0
		lastElementPosition := -1
		menuLastLink := ""
		for child := mainNav.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			lastElementPosition = position
			if child.Data == "a" && hasClass(child, "nav-election-feature") {
				featurePosition = position
			}
			if child.Data == "ul" && hasClass(child, "nav-links") {
				walkNodes(child, func(descendant *html.Node) {
					if descendant.Type == html.ElementNode && descendant.Data == "a" {
						menuLastLink = nodeText(descendant)
					}
				})
			}
			position++
		}
		if featurePosition < 0 || featurePosition != lastElementPosition {
			t.Errorf("desktop Election link position = %d, last navigation position = %d; Election must be rightmost", featurePosition, lastElementPosition)
		}
		if menuLastLink != "Election" {
			t.Errorf("last mobile menu link = %q, want Election", menuLastLink)
		}
	}
	for _, required := range []string{
		"Explore the 2026 ballot",
		"Choose a race to see who's running for mayor, council or school board.",
		"Find and select your ward on the interactive map.",
		"School board",
		"Acclaimed",
		"All five seats were acclaimed.",
		"Donald Pelletier",
		"English Public is the default school-board designation.",
		"Three of the 14 candidates are current at-large councillors",
	} {
		if !strings.Contains(body, required) {
			t.Errorf("rendered page does not contain %q", required)
		}
	}
}


func TestElection2026MetadataAndExternalLinks(t *testing.T) {
	body := renderElection2026(t)
	doc := parseElectionHTML(t, body)

	var title, canonical, ogURL, description string
	badExternalLinks := make([]string, 0)
	walkNodes(doc, func(n *html.Node) {
		if n.Type != html.ElementNode {
			return
		}
		switch n.Data {
		case "title":
			title = nodeText(n)
		case "link":
			if rel, _ := nodeAttr(n, "rel"); rel == "canonical" {
				canonical, _ = nodeAttr(n, "href")
			}
		case "meta":
			if property, _ := nodeAttr(n, "property"); property == "og:url" {
				ogURL, _ = nodeAttr(n, "content")
			}
			if name, _ := nodeAttr(n, "name"); name == "description" {
				description, _ = nodeAttr(n, "content")
			}
		case "a":
			if target, _ := nodeAttr(n, "target"); target == "_blank" {
				rel, _ := nodeAttr(n, "rel")
				if !hasToken(rel, "noopener") {
					href, _ := nodeAttr(n, "href")
					badExternalLinks = append(badExternalLinks, href)
				}
			}
		}
	})

	if title != "2026 Municipal Election - Thunder Citizen" {
		t.Errorf("title = %q", title)
	}
	if canonical != "https://thundercitizen.ca/election/2026" {
		t.Errorf("canonical = %q", canonical)
	}
	if ogURL != canonical {
		t.Errorf("og:url = %q, want canonical %q", ogURL, canonical)
	}
	if description == "" || utf8.RuneCountInString(description) > 160 {
		t.Errorf("description length = %d, content = %q", utf8.RuneCountInString(description), description)
	}
	if len(badExternalLinks) != 0 {
		t.Errorf("target=_blank links without rel=noopener: %#v", badExternalLinks)
	}
}

func TestElection2026ComponentRendersWithoutRequestContext(t *testing.T) {
	var b strings.Builder
	if err := Election2026(views.NewElection2026ViewModel()).Render(context.Background(), &b); err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(b.String(), "2026 Municipal Election") {
		t.Error("rendered component is missing the page title")
	}
}
