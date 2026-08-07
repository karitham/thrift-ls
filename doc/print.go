package doc

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
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

// indentation is the indentation of the current line: the number of
// indent levels, any extra alignment columns, and the display width.
// The prefix string is materialized from the levels on write, so
// commands carry no string copies.
type indentation struct {
	levels int
	alignN int
	length int
}

func (i indentation) add(o Options) indentation {
	i.levels++
	i.length += o.TabWidth

	return i
}

func (i indentation) align(n int, o Options) indentation {
	if o.Indent != "\t" {
		i.alignN += n
		i.length += n

		return i
	}
	// With tabs, a numeric alignment renders as one tab, matching Prettier.
	i.levels++
	i.length += o.TabWidth

	return i
}

// printerPool reuses printer scratch (the output buffer, the fits
// lookahead builder and command stack) across prints; a printer is
// single-threaded and must not be used concurrently. The pool's contents
// are dropped by the GC, so hot paths prefer an Arena's own printer,
// which lives as long as the arena.
var printerPool = sync.Pool{New: func() any { return &printer{} }}

// Print renders doc to a string. Options are validated; a document of an
// unknown shape returns an error (unreachable with the sealed Doc
// interface). Print mutates doc: break propagation sets group break flags.
func Print(d Doc, o Options) (string, error) {
	if err := validateOptions(o); err != nil {
		return "", err
	}

	propagateBreaks(d)

	p := printerPool.Get().(*printer)
	p.reset(o)

	res, err := p.run(d)

	printerPool.Put(p)

	return res, err
}

// Print renders doc with the arena's own printer, whose scratch survives
// the GC as long as the arena does. Like Print, it mutates doc, and the
// arena must not be used concurrently.
func (a *Arena) Print(d Doc, o Options) (string, error) {
	if err := validateOptions(o); err != nil {
		return "", err
	}

	propagateBreaks(d)

	p := &a.printer
	p.reset(o)

	return p.run(d)
}

func validateOptions(o Options) error {
	if o.PrintWidth <= 0 {
		return fmt.Errorf("doc: PrintWidth must be positive, got %d", o.PrintWidth)
	}

	if o.TabWidth <= 0 {
		return fmt.Errorf("doc: TabWidth must be positive, got %d", o.TabWidth)
	}

	return nil
}

// reset prepares a printer for a fresh document, retaining its scratch.
func (p *printer) reset(o Options) {
	if o.NewLine == "" {
		o.NewLine = "\n"
	}

	if o.Indent == "" {
		o.Indent = strings.Repeat(" ", o.TabWidth)
	}

	p.o = o
	p.position = 0
	p.out = p.out[:0]
	if p.groupMode == nil {
		p.groupMode = map[int]mode{}
	} else {
		clear(p.groupMode)
	}
	p.lineSuffix = p.lineSuffix[:0]
	p.shouldRemeasure = false
	p.lastLineComment = false
	p.fitOutput.Reset()
	p.fitCommands = p.fitCommands[:0]
	p.prefixes = p.prefixes[:0]
}

type printer struct {
	fitsDBG         bool
	o               Options
	position        int
	out             []byte
	groupMode       map[int]mode
	lineSuffix      []command
	shouldRemeasure bool
	lastLineComment bool // the last newline written ended a line comment's line

	// fits scratch: the width check is called for every line candidate
	// and would otherwise allocate a builder and a command stack per call.
	fitOutput   strings.Builder
	fitCommands []command
	prefixes    []string // materialized indent prefixes, one per level
}

func (p *printer) write(s string) {
	p.out = append(p.out, s...)
}

// lineEnded reports whether the output already ends with a newline
// (ignoring trailing spaces and tabs, i.e. indentation).
func (p *printer) lineEnded() bool {
	for i := len(p.out) - 1; i >= 0; i-- {
		switch p.out[i] {
		case ' ', '\t':
			continue
		case '\n':
			return true
		default:
			return false
		}
	}

	return false
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

// indentPrefix returns the materialized indentation string for levels
// indent levels, building each level once and caching it.
func (p *printer) indentPrefix(levels int) string {
	if len(p.prefixes) == 0 {
		p.prefixes = append(p.prefixes, "")
	}

	for len(p.prefixes) <= levels {
		p.prefixes = append(p.prefixes, p.prefixes[len(p.prefixes)-1]+p.o.Indent)
	}

	return p.prefixes[levels]
}

// writeIndent writes the indentation of a command: the indent-level
// prefix, then any extra alignment columns.
func (p *printer) writeIndent(i indentation) {
	if i.levels > 0 {
		p.write(p.indentPrefix(i.levels))
	}

	if i.alignN > 0 {
		p.out = append(p.out, strings.Repeat(" ", i.alignN)...)
	}
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

				// Content on the line invalidates the comment-ended mark;
				// a line comment's own CommentLine re-sets it after the
				// comment text.
				p.lastLineComment = false
			}

		case *textNode:
			if v.s != "" {
				p.write(v.s)

				if len(commands) > 0 {
					p.position += stringWidth(v.s)
				}

				p.lastLineComment = false
			}

		case Concat:
			for _, v0 := range slices.Backward(v) {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: v0})
			}

		case *concatNode:
			for _, v0 := range slices.Backward(v.parts) {
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
					p.lastLineComment = false
				} else {
					// A structural soft line right after a line that
					// already ended must not emit another newline — that
					// would be a blank line. After a CommentLine (a line
					// comment owns its line end) the structural
					// indentation is re-applied so the following content
					// lands at the right level (e.g. a closing bracket
					// after a comment inside a list). An AfterComment
					// line collapses only after a CommentLine: a real
					// blank line before it still renders. Hard lines
					// always render (consecutive hard lines are blank
					// lines).
					if !v.Hard && p.lineEnded() && (!v.AfterComment || p.lastLineComment) {
						p.trim()
						p.writeIndent(cmd.indentation)
						p.position = cmd.indentation.length

						break
					}

					p.trim()
					p.write(newLine)
					p.writeIndent(cmd.indentation)
					p.position = cmd.indentation.length
					// A comment's hard line marks the line as comment
					// ended; blank hard lines do not end a content line,
					// so the mark survives them; a rendered structural
					// line starts a fresh content line.
					if v.Comment {
						p.lastLineComment = true
					} else if !v.Hard {
						p.lastLineComment = false
					}
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
	// Retain whatever capacity the loop grew to, so the next fits call
	// starts from the larger backing array.
	defer func() { p.fitCommands = commands[:0] }()

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

		case *textNode:
			if v.s != "" {
				if hasPendingSpace {
					output.WriteString(" ")

					remainingWidth--
					hasPendingSpace = false
				}

				output.WriteString(v.s)
				remainingWidth -= stringWidth(v.s)
			}

		case Concat:
			for _, v0 := range slices.Backward(v) {
				commands = append(commands, command{indentation: cmd.indentation, mode: cmd.mode, doc: v0})
			}

		case *concatNode:
			for _, v0 := range slices.Backward(v.parts) {
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
	case *concatNode:
		for _, part := range v.parts {
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
