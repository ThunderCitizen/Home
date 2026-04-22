package main

import (
	"fmt"
	"os"

	"thundercitizen/internal/munisign"
)

func runHash(args []string) {
	if len(args) != 1 {
		fail("usage: muni hash <dir>")
	}
	dir := args[0]

	hash, err := munisign.HashFS(os.DirFS(dir), map[string]bool{munisign.ManifestFile: true})
	if err != nil {
		fail("hash: %v", err)
	}
	fmt.Println(hash)
}
