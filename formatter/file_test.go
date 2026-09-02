package formatter_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/karitham/thrift-ls/formatter"
	"github.com/karitham/thrift-ls/options"
)

func resolveConfig(path, dir string) (formatter.FormatPatch, error) {
	if path == "" {
		var err error

		path, err = options.FindConfig(dir)
		if err != nil {
			return formatter.FormatPatch{}, err
		}
	}

	if path == "" {
		return formatter.DefaultFormatPatch(), nil
	}

	cfg, err := options.Load(path)
	if err != nil {
		return formatter.FormatPatch{}, err
	}

	return options.Effective(cfg).FormatPatch, nil
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
			wantFile:   "struct API{1:i32 id}",
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
			wantFile:   "struct API{1:i32 id}",
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
			file := filepath.Join(t.TempDir(), "api.thrift")
			require.NoError(t, os.WriteFile(file, []byte("struct API{1:i32 id}"), 0o640))

			var output bytes.Buffer
			err := formatter.FormatFile(file, formatter.FileOptions{
				Output:        &output,
				Write:         tt.write,
				Diff:          tt.diff,
				ResolveConfig: resolveConfig,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutput, output.String())

			content, err := os.ReadFile(file)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFile, string(content))

			info, err := os.Stat(file)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
		})
	}
}

func TestFormatFileRequiresOutputUnlessWriting(t *testing.T) {
	tests := []struct {
		name string
		diff bool
	}{
		{name: "formatted output"},
		{name: "diff output", diff: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "api.thrift")
			source := "struct API{1:i32 id}"
			require.NoError(t, os.WriteFile(file, []byte(source), 0o640))

			err := formatter.FormatFile(file, formatter.FileOptions{Diff: tt.diff})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "output")
			content, readErr := os.ReadFile(file)
			require.NoError(t, readErr)
			assert.Equal(t, source, string(content))
		})
	}
}

func TestFormatFileRequiresResolverForConfigPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "api.thrift")
	source := "struct API{1:i32 id}"
	require.NoError(t, os.WriteFile(file, []byte(source), 0o640))

	var output bytes.Buffer
	err := formatter.FormatFile(file, formatter.FileOptions{
		Output:     &output,
		ConfigPath: "thrift-ls.json",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ConfigPath")
	assert.Contains(t, err.Error(), "ResolveConfig")
	assert.Empty(t, output.String())
	content, readErr := os.ReadFile(file)
	require.NoError(t, readErr)
	assert.Equal(t, source, string(content))
}

func TestFormatFileReturnsParseErrorsWithoutOutput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "api.thrift")
	source := "struct API {"
	require.NoError(t, os.WriteFile(file, []byte(source), 0o644))

	var output bytes.Buffer
	err := formatter.FormatFile(file, formatter.FileOptions{Output: &output, Write: true})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "file does not parse")
	assert.Empty(t, output.String())

	content, readErr := os.ReadFile(file)
	require.NoError(t, readErr)
	assert.Equal(t, source, string(content))
}

func TestFormatAppliesConfigAndPatch(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "api.thrift")
	config := filepath.Join(dir, options.ConfigFileName)
	require.NoError(t, os.WriteFile(file, []byte("struct API { 1: i32 id }"), 0o644))
	require.NoError(t, os.WriteFile(config, []byte(`{"printWidth": 10}`), 0o644))

	width := 80
	tests := []struct {
		name  string
		patch formatter.FormatPatch
		want  string
	}{
		{
			name: "discovered config",
			want: "struct API {\n    1: i32 id\n}\n",
		},
		{
			name:  "patch overrides config",
			patch: formatter.FormatPatch{PrintWidth: &width},
			want:  "struct API { 1: i32 id }\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			err := formatter.FormatFile(file, formatter.FileOptions{
				Output:        &output,
				Patch:         tt.patch,
				ResolveConfig: resolveConfig,
			})
			require.NoError(t, err)
			assert.Equal(t, tt.want, output.String())
		})
	}
}

func TestFormatFileReturnsConfigErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "api.thrift")
	require.NoError(t, os.WriteFile(file, []byte("struct API {}\n"), 0o644))

	tests := []struct {
		name       string
		configPath string
		config     string
		want       string
	}{
		{
			name:       "missing config",
			configPath: filepath.Join(dir, "missing.json"),
			want:       "missing.json",
		},
		{
			name:       "malformed config",
			configPath: filepath.Join(dir, "malformed.json"),
			config:     `{ "printWidth": "wide" }`,
			want:       "malformed.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.config != "" {
				require.NoError(t, os.WriteFile(tt.configPath, []byte(tt.config), 0o644))
			}

			var output bytes.Buffer
			err := formatter.FormatFile(file, formatter.FileOptions{
				Output:        &output,
				ConfigPath:    tt.configPath,
				ResolveConfig: resolveConfig,
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, output.String())
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
		Output: &output,
		Patch:  patch,
	})
	require.NoError(t, err)

	want, err := os.ReadFile(filepath.Join("..", "tests", "e2e", fixture, golden+".expect"))
	require.NoError(t, err)
	assert.Equal(t, string(want), output.String())
}
