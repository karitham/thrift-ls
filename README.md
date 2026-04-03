# Thrift language server

[![Go Coverage](https://github.com/joyme123/thrift-ls/wiki/coverage.svg)](https://raw.githack.com/wiki/joyme123/thrift-ls/coverage.html)
![Go](https://github.com/joyme123/thrift-ls/workflows/Go/badge.svg?branch=main)

thrift-ls implements language server protocol

## features

- highlight
- code completion
- go to definition
- find references
- hover
- dignostic
- rename
- format
- document symbols

## As Thrift Langugae Server

### vim

use thriftls as a lsp provider for thrift

### neovim

You can use [mason](https://github.com/williamboman/mason.nvim) to install thriftls.
And use [nvim-lspconfig](https://github.com/neovim/nvim-lspconfig) to configure thriftls

`:LspInfo` to set lsp information. default log file location: `~/.local/state/nvim/lsp.log`.

![neovim](./doc/image/neovim.png)

### vscode

install thrift-language-server from extension market

![vscode](./doc/image/vscode.png)

## As Thrift Format Tool

**supported flags**

```plaintext
Usage of ./bin/thriftls:
  -align string
        Align enables align option for struct/enum/exception/union fields, Options: "field", "assign", "disable", Default is "field" if not set. (default "field")
  -d	Do not print reformatted sources to standard output. If a file's formatting is different than gofmt's, print diffs to standard output.
  -f string
    	file path to format
  -fieldLineComma string
    	FieldLineComma enables whether to add or remove comma at end of field line. Options: "add", "remove", "disable". If choose disable, user input will be retained without modification. Default is "disable" if not set (default "disable")
  -format
    	use thrift-ls as a format tool
  -indent string
    	Indent to use. Support: num*space, num*tab. example: 4spaces, 1tab, tab (default "4spaces")
  -logLevel int
    	set log level (default -1)
  -trailingNewline
    	Add trailing newline at end of file
  -w	Do not print reformatted sources to standard output. If a file's formatting is different from thriftls's, overwrite it with thrfitls's version.
```

**how to use**

```bash
# format single file
thriftls -format -w -indent 2spaces -f ./tests/galaxy-thrift-api/sds/Table.thrift

# batch format thrift files
find ./tests/galaxy-thrift-api -name "*.thrift" | xargs -n 1 thriftls -format -w -indent 8spaces -f
```

## Configurations

config file location (in order of precedence):

1. `THRIFTLS_CONFIG` env var (if set)
2. `~/.thriftls/config.yaml` on macos/linux
3. `C:\Users\${user}\.thriftls\config.yaml` on Windows

### include_paths

List of additional paths to search for included thrift files. When a thrift file uses `include "foo.thrift"`, thriftls first tries to resolve it relative to the current file's directory. If not found, it searches each path in `include_paths` in order.

This is similar to Apache Thrift's `-I` flag and is useful for monorepos with shared base definitions.

### format

Controls formatting behavior. All format options can be set in the config file and overridden via CLI flags.

Example `~/.thriftls/config.yaml`:

```yaml
logLevel: 3
include_paths:
  - /path/to/base
  - /path/to/shared-types
format:
  indent: "2spaces"
  alignByAssign: "field"
  fieldLineComma: "disable"
  trailingNewline: false
```

#### indent

String specifying indentation style.

Supported formats:
- `<num>spaces`: e.g., `2spaces`, `4spaces`
- `<num>tabs`: e.g., `1tab`, `2tabs`
- `tab` (equivalent to `1tab`)

Default: `4spaces`

#### alignByAssign

Controls alignment of struct/enum/exception/union fields.

Options:
- `field`: Align field IDs, types, and names (default)
- `assign`: Align the `=`sign for default values
- `disable`: No alignment

#### fieldLineComma

Controls trailing commas on field lines.

Options:
- `disable`: Preserve original (default)
- `add`: Always add trailing commas
- `remove`: Remove trailing commas

#### trailingNewline

Controls whether a trailing newline is added at end of file.

Boolean: `true` or `false`

Default: `false` (no trailing newline added)

### logLevel

Controls logging verbosity:

- 1: fatal
- 2: error
- 3: warn (default)
- 4: info
- 5: debug
- 6: trace

## TODO

[] optimize code completion
