package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/joyme123/thrift-ls/format"
	tlog "github.com/joyme123/thrift-ls/log"
	"github.com/joyme123/thrift-ls/lsp"
	"github.com/joyme123/thrift-ls/parser"
	"github.com/joyme123/thrift-ls/utils/diff"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/pkg/fakenet"
)

func fmtFile(opt format.Options, file string) error {
	var stdin = file == "-" || file == ""
	var content []byte
	var err error
	var thrift_file string

	if stdin {
		content, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Println(err)
			return err
		}
		thrift_file = "stdin"
	} else {
		content, err = os.ReadFile(file)
		if err != nil {
			fmt.Println(err)
			return err
		}
		thrift_file = filepath.Base(file)
	}

	ast, err := parser.Parse(thrift_file, content)
	if err != nil {
		fmt.Println(err)
		return err
	}

	formatted, err := format.FormatDocumentWithValidation(ast.(*parser.Document), true)
	if err != nil {
		fmt.Println(err)
		return err
	}

	if opt.Write && !stdin {
		var perms os.FileMode
		fileInfo, err := os.Stat(file)
		if err != nil {
			fmt.Println(err)
			return err
		}
		perms = fileInfo.Mode() // 使用原文件的权限

		// overwrite
		err = os.WriteFile(file, []byte(formatted), perms)
		if err != nil {
			fmt.Println(err)
			return err
		}
	} else {
		if opt.Diff {
			diffLines := diff.Diff("old", content, "new", []byte(formatted))
			fmt.Print(string(diffLines))
		} else {
			fmt.Print(formatted)
		}
		return nil
	}

	return nil

}

func main() {
	formatFile := ""
	flag.StringVar(&formatFile, "f", "", "file path to format")
	formatOpts := format.Options{}
	formatOpts.SetFlags()

	if len(os.Args) > 1 && os.Args[1] == "format" {
		_ = flag.CommandLine.Parse(os.Args[2:])
		formatOpts.InitDefault()

		_ = fmtFile(formatOpts, formatFile)
		return
	}

	flag.Parse()
	tlog.Init(formatOpts.LogLevel)
	formatOpts.InitDefault()

	ctx := context.Background()

	ss := lsp.NewStreamServer()
	stream := jsonrpc2.NewStream(fakenet.NewConn("stdio", os.Stdin, os.Stdout))
	conn := jsonrpc2.NewConn(stream)
	err := ss.ServeStream(ctx, conn)
	if errors.Is(err, io.EOF) {
		return
	}
	panic(err)
}
