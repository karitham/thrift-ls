package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v3"

	"github.com/karitham/thrift-ls/doc"
	"github.com/karitham/thrift-ls/formatter"
	tlog "github.com/karitham/thrift-ls/log"
	"github.com/karitham/thrift-ls/lsp"
	"github.com/karitham/thrift-ls/options"
	"github.com/karitham/thrift-ls/syntax"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/pkg/fakenet"
)

func main() {
	cmd := &cli.Command{
		Name:    "thriftls",
		Usage:   "Thrift language server and formatter",
		Version: lsp.ServerVersion,
		Flags:   lspFlags(),
		// No subcommand: run the language server.
		Action: lspAction,
		Commands: []*cli.Command{
			{
				Name:   "lsp",
				Usage:  "run the language server on stdio",
				Flags:  lspFlags(),
				Action: lspAction,
			},
			{
				Name:      "format",
				Usage:     "format a thrift file",
				ArgsUsage: "<file>",
				Flags:     formatFlags(),
				Action:    formatAction,
			},
			{
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
					&cli.IntFlag{
						Name:  "printWidth",
						Usage: "line width for the IR dump",
						Value: 80,
					},
				},
				Action: dumpAction,
			},
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "thriftls:", err)
		os.Exit(1)
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
			Usage: "path to a thriftls.json config file",
		},
		&cli.StringSliceFlag{
			Name:  "I",
			Usage: "additional include path, like the thrift compiler (repeatable)",
		},
	}
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
			Usage: "path to a thriftls.json config file",
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

// lspAction serves the language server on stdio.
func lspAction(ctx context.Context, cmd *cli.Command) error {
	cfg := loadConfig(cmd.String("config"), ".")
	patch := options.Effective(cfg)

	cli, err := lspPatch(cmd)
	if err != nil {
		return err
	}

	patch = cli.Apply(patch)

	logLevelValue := 3
	if patch.LogLevel != nil {
		logLevelValue = *patch.LogLevel
	}

	tlog.Init(logLevelValue)

	fopts, err := patch.Formatter()
	if err != nil {
		return err
	}

	lspOpts := &lsp.Options{
		IncludePaths: derefStrings(patch.IncludePaths),
		Format:       fopts,
	}

	ss := lsp.NewStreamServer(lspOpts)
	stream := jsonrpc2.NewStream(fakenet.NewConn("stdio", os.Stdin, os.Stdout))
	conn := jsonrpc2.NewConn(stream)

	err = ss.ServeStream(ctx, conn)
	if errors.Is(err, io.EOF) {
		return nil
	}

	return err
}

// formatAction formats a single thrift file.
func formatAction(ctx context.Context, cmd *cli.Command) error {
	file := cmd.Args().First()

	cli, err := formatPatch(cmd)
	if err != nil {
		return err
	}

	return formatFile(file, cmd.Bool("w"), cmd.Bool("d"), cmd.String("config"), cli)
}

// dumpAction prints the parse tree, and optionally the formatted document
// IR with the printer's layout decisions.
func dumpAction(ctx context.Context, cmd *cli.Command) error {
	file := cmd.Args().First()
	if file == "" {
		return errors.New("must specify a thrift file to dump, e.g. thriftls dump file.thrift")
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
		ind, err := options.ParseIndentValue(cmd.String("indent"))
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
				p.Separators = &options.Separators{}
			}

			p.Separators.Set(cf.construct, &v)
		}

		if cmd.IsSet("break-" + cf.name) {
			v := cmd.Bool("break-" + cf.name)

			if p.Break == nil {
				p.Break = &options.Break{}
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
func loadConfig(path, dir string) *options.Patch {
	if path == "" {
		var err error

		path, err = options.FindConfig(dir)
		if err != nil {
			fatal(err)
		}
	}

	if path == "" {
		return nil
	}

	cfg, err := options.Load(path)
	if err != nil {
		fatal(err)
	}

	return cfg
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "thriftls:", err)
	os.Exit(1)
}

// formatFile formats a single file: read, parse, resolve options, format,
// self-validate, and write, diff, or print.
func formatFile(file string, write, diffOut bool, configPath string, cli options.Patch) error {
	if file == "" {
		return errors.New("must specify a thrift file to format, e.g. thriftls format file.thrift")
	}

	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}

	absFile, err := filepath.Abs(file)
	if err != nil {
		return err
	}

	cfg := loadConfig(configPath, filepath.Dir(absFile))
	patch := options.Effective(cfg)
	patch = cli.Apply(patch)

	fopts, err := patch.Formatter()
	if err != nil {
		return err
	}

	parsed, errs := syntax.Parse(src)
	if parseErrors(errs) {
		return fmt.Errorf("%s: file does not parse:\n%s", file, formatErrors(errs))
	}

	out, err := formatter.Format(parsed, fopts)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}

	// Self-validation: the formatted output must parse cleanly.
	if _, errs := syntax.Parse([]byte(out)); parseErrors(errs) {
		return fmt.Errorf("%s: formatting produced invalid output", file)
	}

	switch {
	case write:
		perms := os.FileMode(0o644)
		if info, err := os.Stat(file); err == nil {
			perms = info.Mode()
		}

		return os.WriteFile(file, []byte(out), perms)
	case diffOut:
		fmt.Print(string(Diff("old", src, "new", []byte(out))))

		return nil
	default:
		fmt.Print(out)

		return nil
	}
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
