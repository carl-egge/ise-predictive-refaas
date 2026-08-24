package pyscan

import _ "embed"

// extractPy is the CPython analyzer, embedded so the binary is
// self-contained: the deployment needs an interpreter on PATH, not a copy of
// this repository's source tree.
//
//go:embed extract.py
var extractPy []byte
