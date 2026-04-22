package main

import (
	"os"

	"thundercitizen/internal/munisign"
)

func runVerify(args []string) {
	// Two modes:
	//   verify -key <pubkey> <dir>   — legacy single-key verify
	//   verify -trust <dir>          — use the embedded keys/ trust store
	if len(args) >= 2 && args[0] == "-trust" {
		dir := args[1]
		logf("verifying %s against embedded keys/approved (keys/revoked enforced)\n", dir)
		trust, err := munisign.LoadTrust()
		if err != nil {
			fail("load trust: %v", err)
		}
		v, err := munisign.VerifyFSWithTrust(os.DirFS(dir), trust)
		if err != nil {
			logf("FAIL: %v\n", err)
			os.Exit(1)
		}
		tk := trust.Approved[v.SignerFingerprint]
		logf("OK: merkle root %s\n", v.MerkleRoot)
		logf("signer: %s %s (keys/approved/%s)\n", v.SignerKey, v.SignerFingerprint, tk.Filename)
		return
	}

	keyPath, dir := parseKeyDir(args, "verify")

	logf("verifying %s against %s\n", dir, keyPath)

	pubKey, err := os.ReadFile(keyPath)
	if err != nil {
		fail("read key: %v", err)
	}

	v, err := munisign.VerifyFS(os.DirFS(dir), pubKey)
	if err != nil {
		logf("FAIL: %v\n", err)
		os.Exit(1)
	}

	logf("OK: merkle root %s\n", v.MerkleRoot)
	logf("signer: %s %s\n", v.SignerKey, v.SignerFingerprint)
}

// parseKeyDir extracts -key <path> and trailing <dir> from args.
func parseKeyDir(args []string, cmd string) (string, string) {
	if len(args) < 3 || args[0] != "-key" {
		fail("usage: muni %s -key <keypath> <dir>", cmd)
	}
	return args[1], args[2]
}
