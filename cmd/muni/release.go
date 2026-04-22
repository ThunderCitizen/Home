package main

import (
	"flag"
	"os"
	"path/filepath"

	"thundercitizen/internal/munisign"
)

// runRelease chains extract → sign → publish. Intended as the single command
// operators invoke for a normal bundle release. Each stage uses the same code
// paths as its standalone subcommand; failures bubble up with the same
// messages. Partial failure leaves `out` in a recoverable state — re-running
// is safe.
func runRelease(args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	out := fs.String("out", "data/muni", "bundle directory")
	dryRun := fs.Bool("dry-run", false, "build everything but skip the upload")
	if err := fs.Parse(args); err != nil {
		fail("flags: %v", err)
	}

	logf("release stage 1/3: extract\n")
	runExtract([]string{"-out", *out})

	logf("release stage 2/3: sign\n")
	keyPath, err := autodetectSigningKey()
	if err != nil {
		fail("%v\n\n  drop your approved signer's public key into keys/approved/ so autodetect can find its private sibling in ~/.ssh/", err)
	}
	logf("auto-detected signing key: %s\n", keyPath)
	sig, err := munisign.SignFS(*out, keyPath, logf)
	if err != nil {
		fail("%v", err)
	}
	sigPath := filepath.Join(*out, munisign.ManifestFile)
	if err := os.WriteFile(sigPath, sig, 0o644); err != nil {
		fail("write %s: %v", sigPath, err)
	}
	logf("wrote %s\n", sigPath)

	logf("release stage 3/3: publish\n")
	pubArgs := []string{"-dir", *out}
	if *dryRun {
		pubArgs = append(pubArgs, "-dry-run")
	}
	runPublish(pubArgs)
}
