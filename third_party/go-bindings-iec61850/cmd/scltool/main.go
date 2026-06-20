package main

import (
	"fmt"
	"os"

	"github.com/go-bindings/iec61850/cmd/scltool/cmds"
)

func main() {
	if err := cmds.New().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
