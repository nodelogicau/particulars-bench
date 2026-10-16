// Package prompts holds every prompt the harness sends. A prompt's hash is
// recorded in reports and, for ingest, in the workspace cache key.
//
// The judge-*.md templates reproduce LongMemEval's grading prompts
// (src/evaluation/evaluate_qa.py at commit 9e0b455), Copyright (c) 2024 Di Wu,
// MIT License; see the README.
package prompts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"strings"
)

//go:embed *.md
var files embed.FS

// Get returns the named prompt (file name without .md) without its trailing
// newline. It panics on an unknown name: prompts are compiled in.
func Get(name string) string {
	b, err := files.ReadFile(name + ".md")
	if err != nil {
		panic(err)
	}
	return strings.TrimRight(string(b), "\n")
}

// Hash returns the SHA-256 of the named prompt as Get returns it.
func Hash(name string) string {
	h := sha256.Sum256([]byte(Get(name)))
	return hex.EncodeToString(h[:])
}

// Judge returns the grading template for a question type, abstention
// questions taking the abstention template whatever their type, and the
// template's name.
func Judge(questionType string, abstention bool) (name, template string) {
	switch {
	case abstention:
		name = "judge-abstention"
	case questionType == "temporal-reasoning", questionType == "knowledge-update", questionType == "single-session-preference":
		name = "judge-" + questionType
	default:
		name = "judge-default"
	}
	return name, Get(name)
}
