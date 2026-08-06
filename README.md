# thrift-ls

A Thrift language server and formatter, written from scratch around a lossless
lexer and CST parser, a Prettier-style width-aware formatter, and a clean LSP
implementation.

- **Language server**: completion, go to definition, find references, hover,
  diagnostics, rename, document symbols, and formatting — including
  **range formatting** (Format Selection).
- **Formatter**: a full rewrite of the old template-based formatter. It
  preserves comments and blank lines, understands width, and is deterministic
  and idempotent.

Fork of https://github.com/joyme123/thrift-ls, parser + lexer + formatter rewritten, lsp overhauled

## Installation

```bash
go install github.com/karitham/thrift-ls@latest
```

This installs the `thriftls` binary. It speaks LSP over stdio and doubles as
a CLI formatter.

## Usage

```
thriftls [flags]            run the language server (default)
thriftls lsp [flags]        run the language server
thriftls format [flags] <file>   format a thrift file
```

Run `thriftls --help` or `thriftls format --help` for the full flag list.

### As a language server

`thriftls` is a plain LSP server speaking JSON-RPC over stdio. No editor
extension is required: any LSP client can attach to the binary directly.

```bash
thriftls
```

or, explicitly:

```bash
thriftls lsp
```

#### helix

Helix ships a thrift language definition, so only the server and the
attachment are needed in `~/.config/helix/languages.toml`:

```toml
[language-server.thriftls]
command = "thriftls"

[[language]]
name = "thrift"
language-servers = ["thriftls"]
# optional: format on save via the LSP
auto-format = true
```

`thriftls` must be on `PATH`, or use an absolute path as `command`. The
server logs to `$TMPDIR/thriftls.log`; raise verbosity with `-logLevel`.

#### neovim

Install `thriftls` with [mason](https://github.com/williamboman/mason.nvim)
(`MasonInstall thriftls`), then enable it via
[nvim-lspconfig](https://github.com/neovim/nvim-lspconfig), which ships a
`thriftls` config:

```lua
vim.lsp.enable("thriftls")
```

#### vim

Use `thriftls` as the LSP provider for thrift files:

```vim
let g:lsp_settings = { 'thrift': { 'cmd': ['thriftls'] } }
```

#### vscode

There is no first-party extension. Use any LSP client that accepts a raw
server command, or a generic thrift language extension that lets you point
at your own binary.

### As a formatter

```bash
# print the formatted file to stdout
thriftls format path/to/file.thrift

# overwrite the file in place
thriftls format -w path/to/file.thrift

# print a diff instead
thriftls format -d path/to/file.thrift

# batch-format a tree
find . -name "*.thrift" | xargs -n 1 thriftls format -w
```

Formatting flags:

| Flag                   | Meaning                                                                                            |
| ---------------------- | -------------------------------------------------------------------------------------------------- |
| `-w`                   | Overwrite the file with the formatted result                                                       |
| `-d`                   | Print a diff instead of the formatted result                                                       |
| `--printWidth`         | Target line width (default 80)                                                                     |
| `--indent`             | Indentation: a literal like `"  "` or `"\t"`, a number like `8`, or a legacy spec like `"2spaces"` |
| `--align`              | `field`, `assign`, or `disable`                                                                    |
| `--field-separator`    | Struct/enum field separators: `add`, `remove`, `semicolon`, or `disable` (keep as written)         |
| `--function-separator` | Service arg/throws separators: `add`, `remove`, `semicolon`, or `disable` (keep as written)        |
| `--break-structs`      | Always break struct/union/exception bodies onto multiple lines                                     |
| `--break-enums`        | Always break enum bodies onto multiple lines                                                       |
| `--config`             | Path to a `thriftls.json` config file                                                              |
| `-I`                   | Additional include path, like the thrift compiler's `-I` (repeatable)                              |

Flags override the config file.

## Configuration

Configuration lives in a `thriftls.json` file, discovered by walking up from
the file being formatted or the workspace root (like Biome). Set the
`THRIFTLS_CONFIG` env var to point at an explicit config file.

```json
{
  "printWidth": 100,
  "indent": "  ",
  "tabWidth": 4,
  "align": "field",
  "separators": {
    "fields": "semicolon",
    "functions": "add"
  },
  "break": {
    "structs": true,
    "enums": true
  },
  "includePaths": ["/path/to/base"],
  "logLevel": 3
}
```

### printWidth

Target line width for breaking decisions. Groups (struct bodies, function
signatures, lists, annotations) stay on one line when they fit and break
otherwise, each deciding independently based on the remaining width at its
position. Default: `80`.

### indent

Indentation can be written three ways:

- a literal string: `"  "` (two spaces) or `"\t"` (a tab)
- a number: `8` (eight spaces)
- a legacy spec: `"2spaces"`, `"1tab"` (kept as aliases)

Default: `"    "` (four spaces).

### tabWidth

Display width of a tab when measuring line width. Default: `4`.

### align

Controls column alignment of struct/union/exception fields and enum values.

- `field`: Align field IDs, requiredness, and types (default)
- `assign`: Align the `=` sign for default values
- `disable`: No alignment

### separators

Controls trailing separators for the two field contexts independently.

`separators.fields` — struct/union/exception fields and enum values:

- `disable`: Keep as written (default)
- `add`: Always add trailing commas
- `semicolon`: Always add trailing semicolons
- `remove`: Remove trailing separators

`separators.functions` — service arguments and throws entries, with the
same values:

- `disable`: Keep as written (default)
- `add`: Always add trailing commas
- `semicolon`: Always add trailing semicolons
- `remove`: Remove trailing separators

Broken (multiline) argument and throws blocks are column-aligned like
struct fields, controlled by `align`.

### break

Forces layouts that would otherwise collapse to one line to stay
multiline.

- `break.structs`: Always break struct, union, and exception bodies
- `break.enums`: Always break enum bodies

Both default to `false`.

### includePaths

List of additional paths to search for included thrift files. When a thrift
file uses `include "foo.thrift"`, thrift-ls first tries to resolve it relative
to the current file's directory. If not found, it searches each path in
`includePaths` in order. This is similar to Apache Thrift's `-I` flag.

### logLevel

Controls logging verbosity (the server logs to `$TMPDIR/thriftls.log`):

- 1: fatal
- 2: error
- 3: warn (default)
- 4: info
- 5: debug
- 6: trace

## Development

```bash
go test ./...          # unit and fuzz regression tests
bash tests/e2e/run-e2e.sh   # end-to-end formatter tests
```
