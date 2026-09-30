// transitmetrics archives timetables and rebuilds derived metrics from local data.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"thundercitizen/internal/database"
	"thundercitizen/internal/transit"
)

func main() {
	dir := flag.String("schedule-dir", "", "GTFS directory to archive (does not change the live timetable)")
	source := flag.String("source", "local GTFS", "description/commit identifying the archive")
	published := flag.String("published-at", "", "original capture time, RFC3339 (required when importing)")
	fromText := flag.String("from", "", "first service date to rebuild, YYYY-MM-DD")
	toText := flag.String("to", "", "last service date, inclusive")
	gps := flag.Bool("gps", false, "reconstruct screened timepoint passages from retained GPS first")
	flag.Parse()
	if *dir == "" && *fromText == "" {
		flag.Usage()
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	db, err := database.Connect(ctx, os.Getenv("DATABASE_URL"))
	check(err)
	defer db.Close()
	if *dir != "" {
		at, err := time.Parse(time.RFC3339, *published)
		check(err)
		id, err := transit.ImportMetricSchedule(ctx, db, *dir, *source, at)
		check(err)
		fmt.Printf("Archived schedule %s (%s)\n", id, *source)
	}
	if *fromText == "" {
		return
	}
	from, err := time.ParseInLocation("2006-01-02", *fromText, transit.TZ)
	check(err)
	if *toText == "" {
		*toText = *fromText
	}
	to, err := time.ParseInLocation("2006-01-02", *toText, transit.TZ)
	check(err)
	if to.Before(from) || !to.Before(transit.ServiceDate()) {
		check(fmt.Errorf("choose completed service dates in ascending order"))
	}
	for day := from; !day.After(to); day = day.AddDate(0, 0, 1) {
		started := time.Now()
		n := 0
		if *gps {
			n, err = transit.RebuildMetricPassages(ctx, db, day)
			check(err)
		}
		count, err := transit.BuildChunksForDate(ctx, db, day)
		check(err)
		fmt.Printf("%s: %d passages, %d chunks (%s)\n", day.Format("2006-01-02"), n, count, time.Since(started).Round(time.Millisecond))
	}
}
func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
