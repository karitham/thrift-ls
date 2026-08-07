package formatter

import (
	"os"
	"testing"

	"github.com/karitham/thrift-ls/syntax"
)

var benchSrc = func() []byte {
	b, err := os.ReadFile("../syntax/testdata/bench.thrift")
	if err != nil {
		panic(err)
	}

	return b
}()

func BenchmarkParseFormat(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		doc, errs := syntax.Parse(benchSrc)
		if len(errs) > 0 {
			b.Fatal(errs)
		}

		if _, err := Format(doc, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
