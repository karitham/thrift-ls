// Deterministic regression tests for analyzer bugs found by the property suite.
package analyzers_test

import (
	"testing"

	"github.com/karitham/thrift-ls/sema"
)

func TestIncludeCycleReportedOncePerEdge(t *testing.T) {
	twoFiles := func(p *program) {
		p.add("main.thrift", `include "other.thrift"`)
		p.add("main.thrift", "struct MS {")
		p.add("main.thrift", "  1: OS other,")
		p.add("main.thrift", "}")
		p.add("other.thrift", `include "main.thrift"`)
		p.add("other.thrift", "struct OS {")
		p.add("other.thrift", "  1: MS main,")
		p.add("other.thrift", "}")
	}

	for _, tt := range []struct {
		name  string
		setup func(*program)
		want  map[string]int
	}{
		{
			name:  "both files targeted",
			setup: func(p *program) { twoFiles(p); p.targets = []string{"main.thrift", "other.thrift"} },
			want:  map[string]int{"main.thrift": 1, "other.thrift": 1},
		},
		{
			name:  "one file targeted",
			setup: func(p *program) { twoFiles(p); p.targets = []string{"main.thrift"} },
			want:  map[string]int{"main.thrift": 1, "other.thrift": 1},
		},
		{
			name: "three files targeted",
			setup: func(p *program) {
				p.add("a.thrift", `include "b.thrift"`)
				p.add("b.thrift", `include "c.thrift"`)
				p.add("c.thrift", `include "a.thrift"`)
				p.targets = []string{"a.thrift", "b.thrift", "c.thrift"}
			},
			want: map[string]int{"a.thrift": 1, "b.thrift": 1, "c.thrift": 1},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := newProgram()
			tt.setup(p)

			got := p.collect(t, p.full(t))

			for file, want := range tt.want {
				cycles := 0
				for _, a := range got[file] {
					if a.code == sema.CodeIncludeCycle {
						cycles++
					}
				}

				if cycles != want {
					t.Errorf("%s: %d include-cycle diagnostics, want %d: %v", file, cycles, want, got[file])
				}
			}
		})
	}
}
