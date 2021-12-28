// Copyright (c) 2020 XDC.Network

package main

import (
	"github.com/fatih/color"
	"github.com/BlocksScan/rosetta-XDCNetwork/cmd"
	"os"
)

func main() {
	err := cmd.Execute()
	if err != nil {
		color.Red(err.Error())
		os.Exit(1)
	}
}
