// Package recipes holds the pure, testable time-window calculations. The
// daily input joins and counting rules live in transit/metric_day.go; each
// event source is read once, using an archived timetable for the service date.
package recipes
