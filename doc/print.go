package doc

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Options control how a document is printed.
type Options struct {
	// PrintWidth is the target line width; groups that do not fit within it
	// break. Must be positive.
	PrintWidth int
	// Indent is the string emitted for one indentation level (spaces or a
	// tab). Its display width is TabWidth.
	Indent string
	// TabWidth is the display width of one indentation level. Must be
	// positive.
	TabWidth int
	// NewLine is the line separator, "\n" or "\r\n".
	NewLine string
}

type mode uint8

const (
	modeFlat mode = iota
	modeBreak
)

// command is one unit of work in the printer's stack: a doc to print with
// the mode and indentation it was scheduled under.
type command struct {
	indentation indentation
	mode        mode
	doc         Doc
}

// indentation is the indentation of the current line: the string to emit and its
// display width.
type indentation struct {
	value  string
	length int
}

func (i indentation) add(o Options) indentation {
	if o.Indent == "" {
		o.Indent = strings.Repeat(" ", o.TabWidth)
	}

	i.value += o.Indent
	i.length += o.TabWidth

	return i
}

func (i indentation) align(n int, o Options) indentation {
	if o.Indent != "\t" {
		i.value += strings.Repeat(" ", n)
		i.length += n

		return i
	}
	// With tabs, a numeric alignment renders as one tab, matching Prettier.
	i.value += "\t"
	i.length += o.TabWidth

	return i
}

// Print renders doc to a string. Options are validated; a document of an
// unknown shape returns an error (unreachable with the sealed Doc
// interface). Print mutates doc: break propagation sets group break flags.
func Print(d Doc, o Options) (string, error) {
	if o.PrintWidth <= 0 {
		return "", fmt.Errorf("doc: PrintWidth must be positive, got %d", o.PrintWidth)
	}

	if o.TabWidth <= 0 {
		return "", fmt.Errorf("doc: TabWidth must be positive, got %d", o.TabWidth)
	}

	if o.NewLine == "" {
		o.NewLine = "\n"
	}

	if o.Indent == "" {
		o.Indent = strings.Repeat(" ", o.TabWidth)
	}

	propagateBreaks(d)

	p := &printer{o: o, groupMode: map[int]mode{}}

	return p.run(d)
}

type printer struct {
	fitsDBG         bool
	o               Options
	position        int
	out             []byte
	groupMode       map[int]mode
	lineSuffix      []command
	shouldRemeasure bool

	// fits scratch: the width check is called for every line candidate
	// and would otherwise allocate a builder and a command stack per call.
	fitOutput   strings.Builder
	fitCommands []command
}

func (p *printer) write(s string) {
	p.out = append(p.out, s...)
}

// trim removes trailing spaces and tabs from the output and returns how many
// columns were removed.
func (p *printer) trim() int {
	n := 0

	for _, v := range slices.Backward(p.out) {
		if v != ' ' && v != '\t' {
			break
		}

		n++
	}

	p.out = p.out[:len(p.out)-n]

	return n
}

func (p *printer) run(d Doc) (string, error) {
	commands := []command{{indentation: indentation{}, mode: modeBreak, doc: d}}
	newLine := p.o.NewLine

	for len(commands) > 0 {
		last := len(commands) - 1
		cmd := commands[last]
		commands = commands[:last]

		switch v := cmd.doc.(type) {
		case Text:
			if v != "" {
				s := string(v)
				p.write(s)

				if len(commands) > 0 {
					p.position += stringWidth(s)
				}
			}

		case Concat:
			for _, v0 := range slices.Backward(v) {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: v0})
			}

		case *indent:
			commands = append(commands, command{indentation: cmd.indentation.add(p.o), mode: cmd.mode, doc: v.doc})

		case *align:
			commands = append(commands, command{indentation: cmd.indentation.align(v.n, p.o), mode: cmd.mode, doc: v.doc})

		case trim:
			p.position -= p.trim()

		case *group:
			{
				gcmd := p.printGroup(cmd, v, commands)

				commands = append(commands, gcmd)
				if v.id != 0 {
					p.groupMode[v.id] = gcmd.mode
				}
			}

		case *ifBreak:
			groupMode := cmd.mode
			if v.groupID != 0 {
				if m, ok := p.groupMode[v.groupID]; ok {
					groupMode = m
				} else {
					groupMode = modeFlat
				}
			}

			var contents Doc
			if groupMode == modeBreak {
				contents = v.breakDoc
			} else {
				contents = v.flatDoc
			}

			if contents != nil {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: contents})
			}

		case *lineSuffix:
			p.lineSuffix = append(p.lineSuffix, command{indentation: cmd.indentation, mode: cmd.mode, doc: v.doc})

		case lineSuffixBoundary:
			if len(p.lineSuffix) > 0 {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: HardLineNoBreak})
			}

		case LineDoc:
			switch cmd.mode {
			case modeFlat:
				if !v.Hard {
					if !v.Soft {
						p.write(" ")
						p.position++
					}

					break
				}
				// A hard line printed in flat mode invalidates earlier
				// measurements of enclosing groups; the next group must
				// remeasure.
				p.shouldRemeasure = true

				fallthrough

			case modeBreak:
				if len(p.lineSuffix) > 0 {
					// Print the pending end-of-line suffixes before the
					// newline, then the line itself.
					commands = append(commands, cmd)
					for _, v := range slices.Backward(p.lineSuffix) {
						commands = append(commands, v)
					}

					p.lineSuffix = nil

					break
				}

				if v.Literal {
					p.write(newLine)
					p.position = 0
				} else {
					p.trim()
					p.write(newLine + cmd.indentation.value)
					p.position = cmd.indentation.length
				}
			}

		case breakParent:
			// Handled by propagateBreaks before printing.

		default:
			return "", fmt.Errorf("doc: cannot print unknown document type %T", cmd.doc)
		}

		// Flush remaining suffixes at the end of the document, in case there
		// is no line break after them.
		if len(commands) == 0 && len(p.lineSuffix) > 0 {
			for _, v := range slices.Backward(p.lineSuffix) {
				commands = append(commands, v)
			}

			p.lineSuffix = nil
		}
	}

	return string(p.out), nil
}

// printGroup decides whether g fits in the remaining width and returns the
// command to print it. With expanded states it tries each state in order.
func (p *printer) printGroup(cmd command, g *group, rest []command) command {
	if cmd.mode == modeFlat && !p.shouldRemeasure {
		m := modeFlat
		if g.brk {
			m = modeBreak
		}

		return command{indentation: cmd.indentation, mode: m, doc: g.doc}
	}

	p.shouldRemeasure = false
	remainingWidth := p.o.PrintWidth - p.position
	hasLineSuffix := len(p.lineSuffix) > 0

	flat := command{indentation: cmd.indentation, mode: modeFlat, doc: g.doc}
	if !g.brk && p.fits(flat, rest, remainingWidth, hasLineSuffix) {
		return flat
	}

	if g.expanded == nil {
		return command{indentation: cmd.indentation, mode: modeBreak, doc: g.doc}
	}

	if !g.brk {
		for i := 1; i < len(g.expanded)-1; i++ {
			candidate := command{indentation: cmd.indentation, mode: modeFlat, doc: g.expanded[i]}
			if p.fits(candidate, rest, remainingWidth, hasLineSuffix) {
				return candidate
			}
		}
	}

	return command{indentation: cmd.indentation, mode: modeBreak, doc: g.expanded[len(g.expanded)-1]}
}

// fits reports whether next, followed by the pending rest commands, fits in
// the remaining width. It mirrors Prettier's lookahead: groups are assumed
// flat unless forced, hard lines and line breaks reset the width, and
// line suffixes mark the line as already occupied.
func (p *printer) fits(next command, rest []command, remainingWidth int, hasLineSuffix bool) bool {
	if remainingWidth == math.MaxInt {
		return true
	}

	restIndex := len(rest)
	hasPendingSpace := false
	// output is only used for width counting because trim needs to look
	// backwards for spaces.
	p.fitOutput.Reset()
	output := &p.fitOutput

	p.fitCommands = p.fitCommands[:0]
	commands := append(p.fitCommands, next)

	for remainingWidth >= 0 {
		if p.fitsDBG {
			fmt.Printf("  fitloop rem=%d cmds=%d\n", remainingWidth, len(commands))
		}

		if len(commands) == 0 {
			if restIndex == 0 {
				return true
			}

			restIndex--
			commands = append(commands, rest[restIndex])

			continue
		}

		last := len(commands) - 1
		cmd := commands[last]
		commands = commands[:last]

		switch v := cmd.doc.(type) {
		case Text:
			if v != "" {
				s := string(v)

				if hasPendingSpace {
					output.WriteString(" ")

					remainingWidth--
					hasPendingSpace = false
				}

				output.WriteString(s)
				remainingWidth -= stringWidth(s)
			}

		case Concat:
			for _, v0 := range slices.Backward(v) {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: v0})
			}

		case *indent, *align:
			commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: contentsOf(v)})

		case trim:
			remainingWidth += trimTrailingWidth(output.String())
			output.Reset()

		case *group:
			if v.brk && cmd.mode == modeFlat {
				return false
			}

			groupMode := cmd.mode
			if v.brk {
				groupMode = modeBreak
			}

			contents := v.doc
			if v.expanded != nil && groupMode == modeBreak {
				contents = v.expanded[len(v.expanded)-1]
			}

			commands = append(commands, command{indentation: cmd.indentation, mode: groupMode, doc: contents})

		case *ifBreak:
			groupMode := cmd.mode
			if v.groupID != 0 {
				if m, ok := p.groupMode[v.groupID]; ok {
					groupMode = m
				} else {
					groupMode = modeFlat
				}
			}

			var contents Doc
			if groupMode == modeBreak {
				contents = v.breakDoc
			} else {
				contents = v.flatDoc
			}

			if contents != nil {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: contents})
			}

		case LineDoc:
			if cmd.mode == modeBreak || v.Hard {
				return true
			}

			if !v.Soft {
				hasPendingSpace = true
			}

		case *lineSuffix:
			hasLineSuffix = true

		case lineSuffixBoundary:
			if hasLineSuffix {
				return false
			}
		}
	}

	return false
}

func contentsOf(d Doc) Doc {
	switch v := d.(type) {
	case *indent:
		return v.doc
	case *align:
		return v.doc
	}

	return nil
}

func trimTrailingWidth(s string) int {
	n := 0

	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != ' ' && s[i] != '\t' {
			break
		}

		n++
	}

	return n
}

// propagateBreaks marks groups containing a BreakParent as broken, and
// propagates breaks upward through enclosing groups. Groups are mutated in
// place, matching Prettier's traversal.
func propagateBreaks(d Doc) {
	visited := map[*group]bool{}

	var stack []*group

	enter := func(d Doc) bool {
		switch v := d.(type) {
		case breakParent:
			breakParentGroup(stack)
		case *group:
			stack = append(stack, v)
			if visited[v] {
				return false
			}

			visited[v] = true
		}

		return true
	}
	exit := func(d Doc) {
		if v, ok := d.(*group); ok {
			stack = stack[:len(stack)-1]
			if v.brk {
				breakParentGroup(stack)
			}
		}
	}
	traverseDoc(d, enter, exit, true)
}

func breakParentGroup(stack []*group) {
	if len(stack) > 0 {
		stack[len(stack)-1].brk = true
	}
}

func traverseDoc(d Doc, enter func(Doc) bool, exit func(Doc), includeConditionalGroups bool) {
	if d == nil || !enter(d) {
		return
	}

	switch v := d.(type) {
	case Concat:
		for _, part := range v {
			traverseDoc(part, enter, exit, includeConditionalGroups)
		}
	case *group:
		if includeConditionalGroups {
			for _, state := range v.expanded {
				traverseDoc(state, enter, exit, includeConditionalGroups)
			}
		}

		traverseDoc(v.doc, enter, exit, includeConditionalGroups)
	case *indent:
		traverseDoc(v.doc, enter, exit, includeConditionalGroups)
	case *align:
		traverseDoc(v.doc, enter, exit, includeConditionalGroups)
	case *ifBreak:
		traverseDoc(v.breakDoc, enter, exit, includeConditionalGroups)
		traverseDoc(v.flatDoc, enter, exit, includeConditionalGroups)
	case *lineSuffix:
		traverseDoc(v.doc, enter, exit, includeConditionalGroups)
	}

	exit(d)
}
