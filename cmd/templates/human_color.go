/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package templates

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/mattn/go-isatty"
)

// resolveHumanColor decides whether human-oriented CLI output should emit ANSI styles.
// mode is one of auto, always, or never (case-insensitive). When mode is auto, color is off if
// NO_COLOR is set, on if FORCE_COLOR requests it, otherwise on only when w is a *os.File
// connected to a terminal.
func resolveHumanColor(mode string, w io.Writer) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "auto":
		return humanColorAuto(w), nil
	case "always", "force", "on", "yes":
		return true, nil
	case "never", "off", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid --color %q (use auto, always, or never)", mode)
	}
}

func humanColorAuto(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if fc := strings.ToLower(os.Getenv("FORCE_COLOR")); fc == "1" || fc == "true" || fc == "always" {
		return true
	}
	return writerSupportsANSI(w)
}

func writerSupportsANSI(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// humanPalette holds *color.Color instances for human-readable templates CLI output. When colorize
// is false, every color is built with DisableColor() so Sprint/Fprint emit plain text.
type humanPalette struct {
	stepDefault   *color.Color
	stepHighlight *color.Color
	envLine       *color.Color
	trigLabel     *color.Color
	trigTrue      *color.Color
	trigFalse     *color.Color
	trigErr       *color.Color
	section       *color.Color
	subtle        *color.Color
	phaseOK       *color.Color
	phaseWarn     *color.Color
	phaseBad      *color.Color
	phaseNeutral  *color.Color
	phaseEmpty    *color.Color
	errItem       *color.Color
	errHeader     *color.Color
	prSection     *color.Color
}

func newHumanPalette(colorize bool) humanPalette {
	mk := func(attrs ...color.Attribute) *color.Color {
		c := color.New(attrs...)
		if !colorize {
			c.DisableColor()
		}
		return c
	}
	return humanPalette{
		stepDefault:   mk(color.FgHiCyan),
		stepHighlight: mk(color.FgHiGreen, color.Bold),
		envLine:       mk(color.FgHiWhite),
		trigLabel:     mk(color.Faint),
		trigTrue:      mk(color.FgHiGreen),
		trigFalse:     mk(color.FgHiYellow),
		trigErr:       mk(color.FgHiRed),
		section:       mk(color.FgCyan),
		subtle:        mk(color.Faint),
		phaseOK:       mk(color.FgHiGreen),
		phaseWarn:     mk(color.FgHiYellow),
		phaseBad:      mk(color.FgHiRed),
		phaseNeutral:  mk(color.FgWhite),
		phaseEmpty:    mk(color.Faint),
		errItem:       mk(color.FgHiRed),
		errHeader:     mk(color.FgRed, color.Bold),
		prSection:     mk(color.FgHiCyan, color.Bold),
	}
}

func (p humanPalette) phasePainter(phase string) *color.Color {
	switch strings.ToLower(strings.TrimSpace(phase)) {
	case "success":
		return p.phaseOK
	case "failure":
		return p.phaseBad
	case "pending":
		return p.phaseWarn
	case "":
		return p.phaseEmpty
	default:
		return p.phaseNeutral
	}
}
