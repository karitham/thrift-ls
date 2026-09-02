package formatter_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/formatter"
)

const fileProbe = "struct API{1:i32 id}"

// writeProbe writes source into a fresh temp dir and returns the file path.
func writeProbe(t *testing.T, source string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "api.thrift")
	require.NoError(t, os.WriteFile(file, []byte(source), 0o640))

	return file
}

func readProbe(t *testing.T, file string) string {
	t.Helper()

	content, err := os.ReadFile(file)
	require.NoError(t, err)

	return string(content)
}

func TestFormatOutputModes(t *testing.T) {
	tests := []struct {
		name       string
		write      bool
		diff       bool
		wantOutput string
		wantFile   string
	}{
		{
			name:       "formatted output",
			wantOutput: "struct API { 1: i32 id }\n",
			wantFile:   fileProbe,
		},
		{
			name:     "write in place",
			write:    true,
			wantFile: "struct API { 1: i32 id }\n",
		},
		{
			name:       "diff output",
			diff:       true,
			wantOutput: "diff old new\n--- old\n+++ new\n@@ -1,1 +1,1 @@\n-struct API{1:i32 id}\n\\ No newline at end of file\n+struct API { 1: i32 id }\n",
			wantFile:   fileProbe,
		},
		{
			name:     "write takes precedence over diff",
			write:    true,
			diff:     true,
			wantFile: "struct API { 1: i32 id }\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := writeProbe(t, fileProbe)

			var output bytes.Buffer
			err := formatter.FormatFile(file, formatter.FileOptions{
				Output: &output,
				Write:  tt.write,
				Diff:   tt.diff,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutput, output.String())
			assert.Equal(t, tt.wantFile, readProbe(t, file))

			info, err := os.Stat(file)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
		})
	}
}

// TestFormatFileErrors folds every rejection path into one table: missing
// output, invalid base or override patches, and unparseable input. Each
// case leaves the file untouched.
func TestFormatFileErrors(t *testing.T) {
	bogusAlign := "bogus"
	wideAlign := "wide"

	for _, tt := range []struct {
		name     string
		source   string
		base     *formatter.FormatPatch
		override *formatter.FormatPatch
		diff     bool
		noOutput bool
		write    bool
		wantErr  string
	}{
		{name: "formatted output needs an output", source: fileProbe, noOutput: true, wantErr: "output"},
		{name: "diff output needs an output", source: fileProbe, noOutput: true, diff: true, wantErr: "output"},
		{name: "invalid override", source: fileProbe, override: &formatter.FormatPatch{Align: &bogusAlign}, wantErr: "align"},
		{name: "invalid base", source: "struct API {}\n", base: &formatter.FormatPatch{Align: &wideAlign}, wantErr: "align"},
		{name: "parse error writes nothing", source: "struct API {", write: true, wantErr: "file does not parse"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			file := writeProbe(t, tt.source)

			var output bytes.Buffer
			opts := formatter.FileOptions{Diff: tt.diff, Write: tt.write}
			if !tt.noOutput {
				opts.Output = &output
			}
			if tt.base != nil {
				opts.Base = *tt.base
			}
			if tt.override != nil {
				opts.Override = *tt.override
			}

			err := formatter.FormatFile(file, opts)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, output.String())
			assert.Equal(t, tt.source, readProbe(t, file))
		})
	}
}

// TestFormatFileConfig pins caller-resolved config layering: the Base patch
// carries the resolved file config, the Override patch wins over it.
func TestFormatFileConfig(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "api.thrift")
	require.NoError(t, os.WriteFile(file, []byte("struct API { 1: i32 id }"), 0o644))

	narrow := 10
	base := formatter.FormatPatch{PrintWidth: &narrow}
	width := 80
	for _, tt := range []struct {
		name     string
		override formatter.FormatPatch
		want     string
	}{
		{
			name: "discovered config",
			want: "struct API {\n    1: i32 id\n}\n",
		},
		{
			name:     "patch overrides config",
			override: formatter.FormatPatch{PrintWidth: &width},
			want:     "struct API { 1: i32 id }\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			err := formatter.FormatFile(file, formatter.FileOptions{
				Output:   &output,
				Base:     base,
				Override: tt.override,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, output.String())
		})
	}
}

func TestFormatFileGoldenFields(t *testing.T) {
	tests := []struct {
		name   string
		indent string
		align  string
	}{
		{name: "2spaces.assign", indent: "  ", align: "assign"},
		{name: "2spaces.disable", indent: "  ", align: "disable"},
		{name: "4spaces.field", indent: "    ", align: "field"},
		{name: "tab.assign", indent: "\t", align: "assign"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indent, err := formatter.ParseIndentValue(tt.indent)
			require.NoError(t, err)

			assertGoldenFile(t, "fields", "fields.thrift", tt.name, formatter.FormatPatch{
				Indent: &indent,
				Align:  &tt.align,
			})
		})
	}
}

func TestFormatFileGoldenAnnotations(t *testing.T) {
	comma := "comma"
	tests := []struct {
		name  string
		patch formatter.FormatPatch
	}{
		{name: "annotations"},
		{
			name: "annotations.comma",
			patch: formatter.FormatPatch{
				Separators: &formatter.Separators{Structs: &comma},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGoldenFile(t, "annotations", "annotations.thrift", tt.name, tt.patch)
		})
	}
}

func TestFormatFileGoldenFieldSeparators(t *testing.T) {
	tests := []struct {
		name      string
		separator string
	}{
		{name: "add", separator: "comma"},
		{name: "remove", separator: "none"},
		{name: "disable", separator: "preserve"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertGoldenFile(t, "field_line_comma", "fields.thrift", tt.name, formatter.FormatPatch{
				Separators: &formatter.Separators{Structs: &tt.separator},
			})
		})
	}
}

func TestFormatFileGoldenEnums(t *testing.T) {
	tests := []struct {
		name      string
		indent    string
		align     string
		separator string
	}{
		{name: "2spaces.assign.add", indent: "  ", align: "assign", separator: "comma"},
		{name: "2spaces.disable.remove", indent: "  ", align: "disable", separator: "none"},
		{name: "4spaces.field.disable", indent: "    ", align: "field", separator: "preserve"},
		{name: "tab.assign.disable", indent: "\t", align: "assign", separator: "preserve"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indent, err := formatter.ParseIndentValue(tt.indent)
			require.NoError(t, err)

			assertGoldenFile(t, "enums", "enums.thrift", tt.name, formatter.FormatPatch{
				Indent:     &indent,
				Align:      &tt.align,
				Separators: &formatter.Separators{Enums: &tt.separator},
			})
		})
	}
}

func assertGoldenFile(t *testing.T, fixture, source, golden string, patch formatter.FormatPatch) {
	t.Helper()

	var output bytes.Buffer
	err := formatter.FormatFile(filepath.Join("..", "tests", "e2e", fixture, source), formatter.FileOptions{
		Output:   &output,
		Override: patch,
	})
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("..", "tests", "e2e", fixture, golden+".expect"))
	require.NoError(t, err)
	assert.Equal(t, string(want), output.String())
}
