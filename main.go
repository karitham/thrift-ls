package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/lsp"
	"github.com/karitham/thrift-ls/lsp/cache"
	"github.com/karitham/thrift-ls/lsp/source"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/sema"
	"github.com/karitham/thrift-ls/syntax"

	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

// rootCommand returns the thrift-ls urfave command.
func rootCommand() *cli.Command {
	return &cli.Command{
		Name:    "thrift-ls",
		Usage:   "Thrift language server and formatter",
		Version: lsp.ServerVersion,
		Flags:   lspFlags(),
		// No subcommand: run the language server.
		Action: runLSP,
		Commands: []*cli.Command{
			{
				Name:   "lsp",
				Usage:  "run the language server on stdio",
				Flags:  lspFlags(),
				Action: runLSP,
			},
			{
				Name:      "format",
				Usage:     "format a thrift file",
				ArgsUsage: "<file>",
				Flags:     formatFlags(),
				Action:    formatAction,
			},
			dumpCommand(),
			checkCommand(),
		},
	}
}

func dumpCommand() *cli.Command {
	return &cli.Command{
		Name:      "dump",
		Usage:     "dump the parse tree and document IR of a thrift file",
		ArgsUsage: "<file>",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:  "ir",
				Usage: "also dump the formatted document IR with layout decisions",
			},
			&cli.BoolFlag{
				Name:  "ast",
				Usage: "dump only the parse tree (tokens, trivia, node spans)",
			},
			&cli.BoolFlag{
				Name:  "includes",
				Usage: "show how each include resolves instead of dumping the tree",
			},
			&cli.IntFlag{
				Name:  "printWidth",
				Usage: "line width for the IR dump",
				Value: 80,
			},
		},
		Action: dumpAction,
	}
}

func checkCommand() *cli.Command {
	return &cli.Command{
		Name:      "check",
		Usage:     "report parse, semantic, and lint diagnostics on thrift files",
		ArgsUsage: "<file|folder>",
		Flags:     checkFlags(),
		Action:    checkAction,
	}
}

// lspFlags are the flags shared by the default and lsp subcommands.
func lspFlags() []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:  "logLevel",
			Usage: "set log level (1 fatal .. 6 trace)",
		},
		&cli.StringFlag{
			Name:  "config",
			Usage: "path to a thrift-ls.json config file",
		},
		&cli.StringSliceFlag{
			Name:  "I",
			Usage: "additional include path, like the thrift compiler (repeatable)",
		},
	}
}

// checkFlags are the flags of the check subcommand.
func checkFlags() []cli.Flag {
	return append(lspFlags(), &cli.BoolFlag{
		Name:  "fix",
		Usage: "apply the diagnostics' fixes to the checked files, re-running until nothing applies",
	})
}

// constructFlags maps the per-construct format flag names to constructs.
var constructFlags = []struct {
	name      string
	construct formatter.Construct
}{
	{"struct", formatter.ConstructStruct},
	{"union", formatter.ConstructUnion},
	{"exception", formatter.ConstructException},
	{"enum", formatter.ConstructEnum},
	{"argument", formatter.ConstructArguments},
	{"throws", formatter.ConstructThrows},
	{"list", formatter.ConstructList},
	{"map", formatter.ConstructMap},
	{"set", formatter.ConstructSet},
}

// formatFlags are the flags of the format subcommand.
func formatFlags() []cli.Flag {
	flags := []cli.Flag{
		&cli.BoolFlag{
			Name:  "w",
			Usage: "overwrite the file with the formatted result",
		},
		&cli.BoolFlag{
			Name:  "d",
			Usage: "print a diff instead of the formatted result",
		},
		&cli.IntFlag{
			Name:  "printWidth",
			Usage: "target line width (default 80)",
		},
		&cli.StringFlag{
			Name:  "indent",
			Usage: `indentation: a literal like "  " or "\t"`,
		},
		&cli.StringFlag{
			Name:  "align",
			Usage: `align fields: "field", "assign", or "disable"`,
		},
		&cli.StringFlag{
			Name:  "config",
			Usage: "path to a thrift-ls.json config file",
		},
		&cli.StringSliceFlag{
			Name:  "I",
			Usage: "additional include path, like the thrift compiler (repeatable)",
		},
	}
	for _, cf := range constructFlags {
		flags = append(flags,
			&cli.StringFlag{
				Name:  cf.name + "-separator",
				Usage: fmt.Sprintf("%s separators: \"comma\", \"semicolon\", \"none\", or \"preserve\" to keep as written", cf.name),
			},
			&cli.BoolFlag{
				Name:  "break-" + cf.name,
				Usage: fmt.Sprintf("always break %s bodies onto multiple lines", cf.name),
			},
		)
	}

	return flags
}

// runLSP serves the language server on stdio.
func runLSP(ctx context.Context, cmd *cli.Command) error {
	// Degrade, don't die: editors launch this process, so a broken
	// thrift-ls.json here would kill every buffer's language server at
	// startup. The reason goes to stderr (open before initialize) and
	// per-folder resolution announces it again via window/showMessage.
	cfg := loadConfigLax(cmd.String("config"), ".")
	patch := options.Effective(cfg)

	cliPatch, err := lspPatch(cmd)
	if err != nil {
		return err
	}

	patch = cliPatch.Apply(patch)

	logLevelValue := 3
	if patch.LogLevel != nil {
		logLevelValue = *patch.LogLevel
	}

	lsp.InitLogger(logLevelValue)

	// Validate early: a broken --config or working-directory config must
	// fail before serving; per-folder configs are re-resolved later.
	if err := patch.Validate(); err != nil {
		return err
	}

	lspOpts := &lsp.Options{
		Config:     patch,
		ConfigPath: cmd.String("config"),
		CLI:        cliPatch,
	}

	return lsp.ServeStdio(ctx, lspOpts, os.Stdin, os.Stdout)
}

// formatAction formats a single thrift file.
func formatAction(ctx context.Context, cmd *cli.Command) error {
	file := cmd.Args().First()

	cliPatch, err := formatPatch(cmd)
	if err != nil {
		return err
	}

	return formatter.FormatFile(file, formatter.FileOptions{
		Output:        cmd.Writer,
		Write:         cmd.Bool("w"),
		Diff:          cmd.Bool("d"),
		ConfigPath:    cmd.String("config"),
		Patch:         cliPatch.FormatPatch,
		ResolveConfig: resolveFormatConfig,
	})
}

func resolveFormatConfig(path, dir string) (formatter.FormatPatch, error) {
	cfg, err := loadConfig(path, dir)
	if err != nil {
		return formatter.FormatPatch{}, err
	}

	return options.Effective(cfg).FormatPatch, nil
}

// dumpAction prints the parse tree, and optionally the formatted document
// IR with the printer's layout decisions.
func dumpAction(ctx context.Context, cmd *cli.Command) error {
	file := cmd.Args().First()
	if file == "" {
		return errors.New("must specify a thrift file to dump, e.g. thrift-ls dump file.thrift")
	}

	initCLILogger(cmd)

	if cmd.Bool("includes") {
		return dumpIncludes(ctx, file, cmd)
	}

	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	parsed, errs := syntax.Parse(src)
	if parseErrors(errs) {
		return fmt.Errorf("%s: file does not parse:\n%s", file, formatErrors(errs))
	}

	if !cmd.Bool("ir") {
		fmt.Print(syntax.Dump(parsed))

		return nil
	}

	// The IR dump reflects the printer's layout decisions, so print first
	// (the printer mutates the groups in place), then dump the tree.
	fopts := formatter.DefaultOptions()
	fopts.PrintWidth = cmd.Int("printWidth")

	ir := formatter.BuildIR(parsed, fopts)
	if !cmd.Bool("ast") {
		fmt.Print(syntax.Dump(parsed))
		fmt.Println("--- IR ---")
	}

	if _, err := formatter.PrintIR(ir, fopts); err != nil {
		return err
	}

	fmt.Print(doc.Dump(ir))
	fmt.Println("--- output ---")

	formatted, err := formatter.Format(parsed, fopts)
	if err != nil {
		return err
	}

	fmt.Print(formatted)

	return nil
}

// initCLILogger routes slog records to stderr at the requested --logLevel.
// Without --logLevel the CLI stays silent.
func initCLILogger(cmd *cli.Command) {
	if !cmd.IsSet("logLevel") {
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lsp.SlogLevel(cmd.Int("logLevel")),
	})))
}

// dumpIncludes prints how each include of file resolves: the chosen
// location, its parse status, and the other locations the include path
// matches. It uses the same config and session pipeline as check.
func dumpIncludes(ctx context.Context, file string, cmd *cli.Command) error {
	abs, err := filepath.Abs(file)
	if err != nil {
		return err
	}

	content, err := os.ReadFile(abs)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(cmd.String("config"), filepath.Dir(abs))
	if err != nil {
		return err
	}
	patch := options.Effective(cfg)

	cliPatch, err := lspPatch(cmd)
	if err != nil {
		return err
	}

	patch = cliPatch.Apply(patch)

	fs := cache.NewMemoizedFS()
	sess := cache.NewSession(fs)
	sess.AddView(uri.File(filepath.Dir(abs)), derefStrings(patch.IncludePaths))

	u := uri.File(abs)

	err = sess.UpdateOverlayFS(ctx, []*cache.FileChange{
		{URI: u, Version: 0, Content: content, From: cache.FileChangeTypeDidOpen},
	})
	if err != nil {
		return err
	}

	view, err := sess.ViewOf(u)
	if err != nil {
		return err
	}

	pf, err := view.Parse(ctx, u)
	if err != nil {
		return err
	}

	w := cmd.Writer

	for _, e := range pf.Errors() {
		fmt.Fprintf(w, "parse error: %v\n", e)
	}

	resolver := view.Resolver()

	for _, inc := range pf.AST().Includes() {
		path := inc.PathText()
		if path == "" {
			continue
		}

		candidates := resolver.ResolveIncludeCandidates(u, path)
		fmt.Fprintf(w, "%s\n", path)

		if len(candidates) == 0 {
			fmt.Fprintf(w, "  not found\n")

			continue
		}

		target, terr := view.Parse(ctx, candidates[0])
		switch {
		case terr != nil:
			fmt.Fprintf(w, "  resolved: %s (unreadable: %v)\n", candidates[0].FsPath(), terr)
		default:
			fmt.Fprintf(w, "  resolved: %s (%d parse errors)\n", candidates[0].FsPath(), len(target.Errors()))
		}

		for _, cand := range candidates[1:] {
			fmt.Fprintf(w, "  also matches: %s\n", cand.FsPath())
		}
	}

	return nil
}

// checkAction reports the diagnostics the language server computes for a
// thrift file or folder, through the same cache and checker pipeline the
// LSP uses. Diagnostics print to stdout; the command exits 1 when any
// error-severity diagnostic is found, so it can gate CI. Warnings do not
// fail the check.
func checkAction(ctx context.Context, cmd *cli.Command) error {
	path := cmd.Args().First()
	if path == "" {
		return errors.New("must specify a thrift file or folder to check, e.g. thrift-ls check file.thrift")
	}

	initCLILogger(cmd)

	files, err := collectThriftFiles(path)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		return fmt.Errorf("no thrift files found in %s", path)
	}

	cfg, err := loadConfig(cmd.String("config"), ".")
	if err != nil {
		return err
	}
	patch := options.Effective(cfg)

	cliPatch, err := lspPatch(cmd)
	if err != nil {
		return err
	}

	patch = cliPatch.Apply(patch)

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	root := path
	if !info.IsDir() {
		root = filepath.Dir(path)
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	if cmd.Bool("fix") {
		return checkFix(ctx, cmd, files, rootAbs, derefStrings(patch.IncludePaths), lintConfigOf(patch.Lint))
	}

	diags, err := checkFiles(ctx, files, rootAbs, derefStrings(patch.IncludePaths), lintConfigOf(patch.Lint))
	if err != nil {
		return err
	}

	w := cmd.Writer
	errCount, warnCount := 0, 0

	for _, file := range files {
		for _, d := range diags[file] {
			sev := "warning"
			if d.Severity == protocol.DiagnosticSeverityError {
				sev = "error"
				errCount++
			} else {
				warnCount++
			}

			fmt.Fprintf(w, "%s:%d:%d  %s  %s\n", relPath(file), d.Range.Start.Line+1, d.Range.Start.Character+1, sev, d.Message)
		}
	}

	if errCount > 0 {
		return fmt.Errorf("found %d error(s), %d warning(s)", errCount, warnCount)
	}

	return nil
}

// checkFiles runs the language server's diagnostic pipeline — parse,
// semantic analysis, and lints — over files opened in a session rooted at
// folder, and returns the diagnostics per file, keyed by absolute path.
func checkFiles(ctx context.Context, files []string, folder string, includePaths []string, lint sema.Config) (map[string][]protocol.Diagnostic, error) {
	_, view, uris, err := openCheckSession(ctx, files, folder, includePaths)
	if err != nil {
		return nil, err
	}

	out := make(map[string][]protocol.Diagnostic, len(files))

	// One pipeline run over the whole corpus: the shared index memoizes
	// resolutions across files, so each name resolves once.
	report, err := sema.DefaultPipeline(lint).Run(ctx, view, uris)
	if err != nil {
		return nil, err
	}

	for i := range files {
		diags, err := source.ToProtocolDiagnostics(ctx, view, uris[i], report[uris[i]])
		if err != nil {
			return nil, err
		}

		out[files[i]] = diags
	}

	return out, nil
}

// checkFix applies the diagnostics' fixes to the checked files and reports
// what remains. Only the requested files are fixed — one file, or one
// folder — while resolution reads the whole view, so fixing a greenfield
// module resolves its types against the tree without touching the tree.
func checkFix(ctx context.Context, cmd *cli.Command, files []string, folder string, includePaths []string, lint sema.Config) error {
	sess, view, uris, err := openCheckSession(ctx, files, folder, includePaths)
	if err != nil {
		return err
	}

	// Fix passes land within the same mtime tick the memoized disk source
	// may have cached, so the fixed content flows back through the
	// session overlay: the next pass always re-parses what was written.
	version := 0

	persist := func(ctx context.Context, u uri.URI, content []byte) error {
		version++

		if err := sess.UpdateOverlayFS(ctx, []*cache.FileChange{
			{URI: u, Version: version, Content: content, From: cache.FileChangeTypeDidChange},
		}); err != nil {
			return err
		}

		perms := os.FileMode(0o644)
		if info, statErr := os.Stat(u.FsPath()); statErr == nil {
			perms = info.Mode()
		}

		return os.WriteFile(u.FsPath(), content, perms)
	}

	res, err := sema.DefaultPipeline(lint).FixAll(ctx, view, uris, persist)
	if err != nil {
		return err
	}

	w := cmd.Writer

	fmt.Fprintf(w, "applied %d fix(es) in %d file(s) over %d pass(es)\n", res.Applied, len(res.FixedFiles), res.Passes)

	for _, s := range res.Skipped {
		fmt.Fprintf(w, "skipped %s  %s  (%s)\n", relPath(s.File.FsPath()), s.Fix.Title, s.Reason)
	}

	errCount, warnCount := 0, 0

	for i := range files {
		for _, d := range res.Remaining[uris[i]] {
			sev := "warning"
			if d.Severity == sema.SeverityError {
				sev = "error"
				errCount++
			} else {
				warnCount++
			}

			fmt.Fprintf(w, "%s:%d:%d  %s  %s\n", relPath(uris[i].FsPath()), d.Span.Start.Line, d.Span.Start.Col, sev, d.Message)
		}
	}

	if errCount > 0 {
		return fmt.Errorf("%d error(s), %d warning(s) remain unfixed", errCount, warnCount)
	}

	return nil
}

// openCheckSession opens a session with the files open in the overlay of
// a view rooted at folder, and returns the session (needed to push new
// content into the overlay later), its view, and the files' URIs.
func openCheckSession(ctx context.Context, files []string, folder string, includePaths []string) (*cache.Session, *cache.View, []uri.URI, error) {
	sess := cache.NewSession(cache.NewMemoizedFS())
	view := sess.AddView(uri.File(folder), includePaths)

	changes := make([]*cache.FileChange, 0, len(files))
	uris := make([]uri.URI, 0, len(files))

	for _, file := range files {
		content, err := os.ReadFile(file)
		if err != nil {
			return nil, nil, nil, err
		}

		u := uri.File(file)
		uris = append(uris, u)
		changes = append(changes, &cache.FileChange{URI: u, Version: 0, Content: content, From: cache.FileChangeTypeDidOpen})
	}

	if err := sess.UpdateOverlayFS(ctx, changes); err != nil {
		return nil, nil, nil, err
	}

	return sess, view, uris, nil
}

// collectThriftFiles returns the absolute paths of the thrift files under
// path: the file itself, or every *.thrift in the folder, recursively, in
// lexical order.
func collectThriftFiles(path string) ([]string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}

	if !info.IsDir() {
		return []string{abs}, nil
	}

	var files []string

	err = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".thrift") {
			files = append(files, p)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(files)

	return files, nil
}

// relPath renders p relative to the working directory when it lies under
// it, and absolute otherwise.
func relPath(p string) string {
	base, err := os.Getwd()
	if err != nil {
		return p
	}

	rel, err := filepath.Rel(base, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}

	return rel
}

// lspPatch builds an options patch from the explicitly set lsp flags.
func lspPatch(cmd *cli.Command) (options.Patch, error) {
	p := options.Patch{}

	if cmd.IsSet("logLevel") {
		v := cmd.Int("logLevel")
		p.LogLevel = &v
	}

	if paths := cmd.StringSlice("I"); len(paths) > 0 {
		p.IncludePaths = &paths
	}

	return p, nil
}

// formatPatch builds an options patch from the explicitly set format flags.
func formatPatch(cmd *cli.Command) (options.Patch, error) {
	p := options.Patch{}

	if cmd.IsSet("printWidth") {
		v := cmd.Int("printWidth")
		p.PrintWidth = &v
	}

	if cmd.IsSet("indent") {
		ind, err := formatter.ParseIndentValue(cmd.String("indent"))
		if err != nil {
			return options.Patch{}, err
		}

		p.Indent = &ind
	}

	if cmd.IsSet("align") {
		v := cmd.String("align")
		p.Align = &v
	}

	for _, cf := range constructFlags {
		if cmd.IsSet(cf.name + "-separator") {
			v := cmd.String(cf.name + "-separator")

			if p.Separators == nil {
				p.Separators = &formatter.Separators{}
			}

			p.Separators.Set(cf.construct, &v)
		}

		if cmd.IsSet("break-" + cf.name) {
			v := cmd.Bool("break-" + cf.name)

			if p.Break == nil {
				p.Break = &formatter.Break{}
			}

			p.Break.Set(cf.construct, &v)
		}
	}

	if paths := cmd.StringSlice("I"); len(paths) > 0 {
		p.IncludePaths = &paths
	}

	return p, nil
}

// loadConfig loads the explicit config path, or finds one walking up from
// dir. A missing config is not an error.
func loadConfig(path, dir string) (*options.Patch, error) {
	if path == "" {
		var err error

		path, err = options.FindConfig(dir)
		if err != nil {
			return nil, err
		}
	}

	if path == "" {
		return nil, nil
	}

	cfg, err := options.Load(path)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

// loadConfigLax is loadConfig for the language server: any failure degrades
// to nil (defaults) after printing why, never os.Exit — see runLSP. The
// strict variant stays for format/check, where a config typo should fail
// fast rather than silently reformat against defaults.
func loadConfigLax(path, dir string) *options.Patch {
	say := func(err error) {
		fmt.Fprintf(os.Stderr, "thrift-ls: %v; continuing with defaults\n", err)
	}

	found := path

	if found == "" {
		var err error

		found, err = options.FindConfig(dir)
		if err != nil {
			say(err)

			return nil
		}
	}

	if found == "" {
		return nil
	}

	cfg, err := options.Load(found)
	if err != nil {
		say(err)

		return nil
	}

	return cfg
}

func parseErrors(errs []syntax.Error) bool {
	for _, e := range errs {
		if e.Severity == syntax.SeverityError {
			return true
		}
	}

	return false
}

func formatErrors(errs []syntax.Error) string {
	var b strings.Builder

	for _, e := range errs {
		if e.Severity == syntax.SeverityError {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}

	return b.String()
}

func derefStrings(p *[]string) []string {
	if p == nil {
		return nil
	}

	return *p
}

// lintConfigOf converts the config layer's lint settings into the
// pipeline's config. The options package stays plain data, so each
// frontend owns this translation; sema.ConfigFromLint does the work.
func lintConfigOf(l *options.LintConfig) sema.Config {
	if l == nil {
		return sema.Config{}
	}

	var disabled []string
	if l.Disabled != nil {
		disabled = *l.Disabled
	}

	var severity map[string]string
	if l.Severity != nil {
		severity = *l.Severity
	}

	return sema.ConfigFromLint(disabled, severity)
}

func main() {
	if err := rootCommand().Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "thrift-ls:", err)
		os.Exit(1)
	}
}
