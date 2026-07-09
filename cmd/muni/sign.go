package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"

	"thundercitizen/internal/munisign"
)

func runSign(args []string) {
	keyPath, dir := parseSignArgs(args)

	sig, err := munisign.SignFS(dir, keyPath, logf)
	if err != nil {
		fail("%v", err)
	}

	outPath := filepath.Join(dir, munisign.ManifestFile)
	if err := os.WriteFile(outPath, sig, 0o644); err != nil {
		fail("write %s: %v", outPath, err)
	}
	logf("wrote %s\n", outPath)
}

// parseSignArgs supports three forms:
//
//	muni sign <dir>              — autodetect from keys/approved/
//	muni sign -key <k> <dir>     — explicit private key path
//	muni sign -key <k>           — (error) dir required
//
// Autodetect walks approved signers in trust-store filename order, then
// finds the matching ~/.ssh/*.pub and private sibling. This lets key
// rotation prefer a newer approved signer by naming it earlier.
func parseSignArgs(args []string) (string, string) {
	if len(args) >= 3 && args[0] == "-key" {
		return args[1], args[2]
	}
	if len(args) == 1 {
		keyPath, err := autodetectSigningKey()
		if err != nil {
			fail("%v\n\n  pass -key <privkey> to be explicit, or drop your approved signer's public key into keys/approved/ so autodetect can find its private sibling in ~/.ssh/", err)
		}
		logf("auto-detected signing key: %s\n", keyPath)
		return keyPath, args[0]
	}
	fail("usage: muni sign [-key <keypath>] <dir>")
	return "", ""
}

// autodetectSigningKey returns the private key under ~/.ssh/ whose public
// half matches the first approved signer in keys/approved/ filename order.
// The match is strict: fingerprint equality, not filename heuristics.
func autodetectSigningKey() (string, error) {
	trust, err := munisign.LoadTrust()
	if err != nil {
		return "", fmt.Errorf("load trust: %w", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	sshDir := filepath.Join(home, ".ssh")
	entries, err := os.ReadDir(sshDir)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", sshDir, err)
	}

	byFingerprint := make(map[string]string)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".pub") {
			continue
		}
		pubPath := filepath.Join(sshDir, name)
		data, err := os.ReadFile(pubPath)
		if err != nil {
			continue
		}
		k, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err != nil {
			continue
		}
		fp := ssh.FingerprintSHA256(k)
		byFingerprint[fp] = pubPath
	}

	approved, _ := trust.Summary()
	for _, signer := range approved {
		pubPath, ok := byFingerprint[signer.Fingerprint]
		if !ok {
			continue
		}
		privPath := strings.TrimSuffix(pubPath, ".pub")
		if _, err := os.Stat(privPath); err != nil {
			return "", fmt.Errorf("found approved pubkey %s but private sibling %s is missing", pubPath, privPath)
		}
		return privPath, nil
	}
	return "", fmt.Errorf("no private key in %s matches any fingerprint in keys/approved/", sshDir)
}
