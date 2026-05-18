// Package main contains the entrypoint for the converter service and a few
// small helpers used by tests and validation.
package main

import (
	"strings"
	"unicode"

	log "github.com/sirupsen/logrus"
)

const OLLAMA_API_URL = "http://localhost:11434"

// main starts the converter service.
func main() {
	log.SetLevel(log.DebugLevel)
	err := MakeConverterService()
	if err != nil {
		panic(err)
	}
}

// MinimizeString removes control characters from `s` and returns a
// compact, human-readable string useful for comparing outputs.
func MinimizeString(s string) string {
	var builder strings.Builder
	for _, r := range s {
		if !unicode.IsControl(r) {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}
