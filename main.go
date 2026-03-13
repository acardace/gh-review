package main

import (
	"os"

	"github.com/acardace/gh-review/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
