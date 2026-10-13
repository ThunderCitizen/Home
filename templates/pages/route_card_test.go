package pages

import (
	"bytes"
	"strings"
	"testing"

	"thundercitizen/internal/transit"
)

func TestRouteCardParentFieldsAndLiveHooks(t *testing.T) {
	meta := transit.RouteMetaAPI{RouteID: "1", Name: "Mainline"}
	for _, tc := range []struct {
		name        string
		interactive bool
		fields      []routeCardField
		want        []string
	}{
		{"live", true, []routeCardField{{Value: "3", Label: "buses"}, liveCancelField(2, "cancel", "ft-error", "1"), liveCancelField(0, "upcoming", "ft-warn", "1")}, []string{`<button`, `data-route="1"`, `data-cancel-route="1"`, `>buses<`, `>upcoming<`}},
		{"history", false, []routeCardField{{Value: "20", Label: "trips"}, {Value: "—", Label: "cancel"}, {Value: "75%", Label: "OTP"}}, []string{`<div`, `>trips<`, `>OTP<`, `>—<`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := routeCard(meta, false, tc.interactive, tc.fields).Render(t.Context(), &b); err != nil {
				t.Fatal(err)
			}
			html := b.String()
			for _, want := range tc.want {
				if !strings.Contains(html, want) {
					t.Errorf("missing %s: %s", want, html)
				}
			}
			if strings.Count(html, `class="route-pill-ft-val"`) != len(tc.fields) {
				t.Fatal("field count changed")
			}
			if !tc.interactive && (strings.Contains(html, "data-cancel-route") || strings.Contains(html, "<a ") || strings.Contains(html, "<button")) {
				t.Fatal("live hooks leaked into history")
			}
		})
	}
}
