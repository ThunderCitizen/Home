package pages

import (
	"reflect"
	"testing"
)

func TestSegmentAlertTextLinksTransitURL(t *testing.T) {
	got := segmentAlertText("For details visit thunderbay.ca/transit.")
	want := []alertSegment{
		{Text: "For details visit "},
		{Text: "thunderbay.ca/transit", Href: "https://thunderbay.ca/transit"},
		{Text: "."},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segmentAlertText() = %#v, want %#v", got, want)
	}
}

func TestSegmentAlertTextLeavesOtherTextAlone(t *testing.T) {
	got := segmentAlertText("For details visit example.com/transit.")
	want := []alertSegment{{Text: "For details visit example.com/transit."}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segmentAlertText() = %#v, want %#v", got, want)
	}
}
