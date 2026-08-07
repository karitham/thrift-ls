package syntax

import (
	"os"
	"testing"
)

var benchSrc = func() []byte {
	b, err := os.ReadFile("testdata/bench.thrift")
	if err != nil {
		panic(err)
	}

	return b
}()

func BenchmarkParse(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = Parse(benchSrc)
	}
}

func BenchmarkLex(b *testing.B) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_, _ = Lex(benchSrc)
	}
}
