package main

import "os"

func pinCLI(args []string) {
	os.Exit(archiveMain("pin", args, os.Stdout))
}

func unpinCLI(args []string) {
	os.Exit(archiveMain("unpin", args, os.Stdout))
}
