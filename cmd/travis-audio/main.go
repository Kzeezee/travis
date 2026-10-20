// Command travis-audio is the voice helper process (DESIGN.md §5.10): it owns the microphone so
// the travis binary stays free of cgo. `travis-audio capture` streams voicein's PCM format to
// stdout until stdin closes, which is how travis stops it.
package main

import (
	"fmt"
	"os"
)

const usage = "usage: travis-audio capture"

func main() {
	if len(os.Args) != 2 || os.Args[1] != "capture" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err := captureUntilStdinCloses(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "travis-audio:", err)
		os.Exit(1)
	}
}
