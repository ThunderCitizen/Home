package main

import (
	"fmt"

	"thundercitizen/internal/council"
)

func runVotes() {
	ctx, cancel := rootContext()
	defer cancel()

	opts := council.VotesFetchOptions{} // always: all terms, full download

	fmt.Println("Discovering meetings via eSCRIBE...")
	sources, err := council.DiscoverVoteSources(ctx, opts)
	if err != nil {
		fail("discover: %v", err)
	}
	if len(sources) == 0 {
		fmt.Println("No meetings to fetch.")
		return
	}
	printSources(sources)

	if !confirm() {
		fmt.Println("cancelled")
		return
	}

	pool, err := openPool(ctx)
	if err != nil {
		fail("db: %v", err)
	}
	defer pool.Close()

	if err := council.FetchVotes(ctx, opts, pool); err != nil {
		fail("fetch: %v", err)
	}

	// The /minutes footer shows a hand-maintained "last checked <date>"
	// stamp (deliberately not render-time — see minutesLastChecked in
	// templates/pages/motions.templ). You just verified upstream, so bump it.
	fmt.Println()
	fmt.Println("REMINDER: update minutesLastChecked in templates/pages/motions.templ")
	fmt.Println("          to today's date so the /minutes footer reflects this check.")
}
