// Package validations holds the parts of tag-driven promotion ordering that need no I/O: how a
// validation is written in a git note, and the rules that decide where an environment's tag points.
//
// A validation is a record that a dry commit may deploy to an environment. The git package reads and
// writes validations; the ValidatedCommitStatus and CommitValidation controllers decide what they mean.
package validations

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
)

// Sources of a validation, as written in the "source" field of a note line.
const (
	// SourceResource marks a validation submitted through a CommitValidation.
	SourceResource = "Resource"
	// SourceAutomatic marks a validation the ValidatedCommitStatus controller wrote once every upstream
	// environment succeeded on the commit.
	SourceAutomatic = "Automatic"
	// SourceGit marks a validation pushed directly as a git note. A line that names no source, or one
	// this package does not know, is read as SourceGit.
	SourceGit = "Git"
)

// tagSuffix is appended to an environment branch to name its tag, as "-next" names its proposed branch.
const tagSuffix = "-tag"

// TagName returns the name of the tag an environment's hydrator reads: <branch>-tag, or
// <branch>-tag/<activePath> when the environment has an activePath. It mirrors the proposed branch
// convention, so tag names are unique whenever proposed branches are.
func TagName(branch, activePath string) string {
	tag := branch + tagSuffix
	if activePath != "" {
		tag = path.Join(tag, activePath)
	}
	return tag
}

// Line is one validation as it is stored in a git note on the validated dry commit. A note holds one
// JSON line per validation, so two writers never edit the same line and git notes append works.
type Line struct {
	// At is when the validation was made. Zero when the line does not say; the notes commit's date is
	// used then.
	At time.Time
	// Target is the name of the environment tag the commit is validated for. A tag name is unique to
	// one PromotionStrategy, where an environment branch may be shared by many.
	Target string
	// Source is SourceResource, SourceAutomatic or SourceGit.
	Source string
	// By names who or what validated the commit. Empty when the line does not say; the author of the
	// notes commit is used then.
	By string
	// Reason is free text given with the validation.
	Reason string
	// ID identifies the request that produced the line, so a writer that retries does not record the
	// same request twice. Optional.
	ID string
}

// wireLine is the JSON form of a Line. Only target is required.
type wireLine struct {
	Target string `json:"target"`
	Source string `json:"source,omitempty"`
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
	At     string `json:"at,omitempty"`
	ID     string `json:"id,omitempty"`
}

// Automatic reports whether the validation is an automatic one, which only moves a tag forward. Every
// other validation is manual and moves the tag wherever it says.
func (l Line) Automatic() bool {
	return l.Source == SourceAutomatic
}

// Marshal returns the line as it is written to a note: one JSON object with no newline.
func (l Line) Marshal() (string, error) {
	if l.Target == "" {
		return "", errors.New("validation has no target")
	}
	wire := wireLine{Target: l.Target, Source: l.Source, By: l.By, Reason: l.Reason, ID: l.ID}
	if !l.At.IsZero() {
		wire.At = l.At.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("failed to marshal validation: %w", err)
	}
	return string(data), nil
}

// ParseLine reads one line of a note. It returns false for anything that is not a validation: a blank
// line, text that is not a JSON object, or an object with no target. People write these lines by
// hand, so a line is never an error; it is a validation or it is ignored.
func ParseLine(text string) (Line, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "{") {
		return Line{}, false
	}
	var wire wireLine
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return Line{}, false
	}
	wire.Target = strings.TrimSpace(wire.Target)
	if wire.Target == "" {
		return Line{}, false
	}

	line := Line{Target: wire.Target, Source: SourceGit, By: wire.By, Reason: wire.Reason, ID: wire.ID}
	if wire.Source == SourceResource || wire.Source == SourceAutomatic {
		line.Source = wire.Source
	}
	// An unreadable time is dropped rather than making the validation invalid.
	if at, err := time.Parse(time.RFC3339, wire.At); err == nil {
		line.At = at
	}
	return line, true
}

// ParseNote returns the validations in a note, in the order they appear.
func ParseNote(content string) []Line {
	var lines []Line
	for text := range strings.SplitSeq(content, "\n") {
		if line, ok := ParseLine(text); ok {
			lines = append(lines, line)
		}
	}
	return lines
}
