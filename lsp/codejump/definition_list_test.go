package codejump

import (
	"context"
	"testing"

	"github.com/joyme123/protocol"
	"github.com/joyme123/thrift-ls/lsp/cache"
	"github.com/stretchr/testify/assert"
	"go.lsp.dev/uri"
)

func TestDefinition_EnumValueInConstList(t *testing.T) {
	// Test case for go-to-definition on enum values inside const lists
	// This reproduces the issue where clicking on MyEnum.Value1 inside
	// const list<MyEnum> my_list = [MyEnum.Value1] doesn't jump to the definition
	file := `enum MyEnum {
  Value1 = 1,
  Value2 = 2,
  Value3 = 3
}

const list<MyEnum> my_list = [MyEnum.Value1, MyEnum.Value2]`

	ss := cache.BuildSnapshotForTest([]*cache.FileChange{
		{
			URI:     "file:///tmp/test.thrift",
			Version: 0,
			Content: []byte(file),
			From:    cache.FileChangeTypeDidOpen,
		},
	})

	type args struct {
		ctx  context.Context
		ss   *cache.Snapshot
		file uri.URI
		pos  protocol.Position
	}
	tests := []struct {
		name      string
		args      args
		want      []protocol.Location
		assertion assert.ErrorAssertionFunc
	}{
		{
			name: "enum value in const list - first element",
			args: args{
				ctx:  context.TODO(),
				ss:   ss,
				file: "file:///tmp/test.thrift",
				pos: protocol.Position{
					Line:      6,  // Line with "const list<MyEnum> my_list = [MyEnum.Value1, MyEnum.Value2]"
					Character: 38, // Position inside "Value1" in "MyEnum.Value1" (column 37 is 'V')
				},
			},
			want: []protocol.Location{
				{
					URI: "file:///tmp/test.thrift",
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      1, // Line with "Value1 = 1"
							Character: 2, // Start of "Value1"
						},
						End: protocol.Position{
							Line:      1,
							Character: 9, // End of "Value1"
						},
					},
				},
			},
			assertion: assert.NoError,
		},
		{
			name: "enum value in const list - second element",
			args: args{
				ctx:  context.TODO(),
				ss:   ss,
				file: "file:///tmp/test.thrift",
				pos: protocol.Position{
					Line:      6,
					Character: 53, // Position inside "Value2" in "MyEnum.Value2" (column 52 is 'V')
				},
			},
			want: []protocol.Location{
				{
					URI: "file:///tmp/test.thrift",
					Range: protocol.Range{
						Start: protocol.Position{
							Line:      2, // Line with "Value2 = 2"
							Character: 2, // Start of "Value2"
						},
						End: protocol.Position{
							Line:      2,
							Character: 9, // End of "Value2"
						},
					},
				},
			},
			assertion: assert.NoError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Definition(tt.args.ctx, tt.args.ss, tt.args.file, tt.args.pos)
			tt.assertion(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
