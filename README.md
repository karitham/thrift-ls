# thrift-ls

A Thrift language server and formatter, written from scratch around a lossless
lexer and CST parser, a Prettier-style width-aware formatter, and a clean LSP
implementation.

- **Language server**: completion, go to definition, find references, hover,
  diagnostics, rename, document symbols, and formatting — including
  **range formatting** (Format Selection).
- **Formatter**: a full rewrite of the old template-based formatter. It is
  lossless (comments, annotations, and blank lines survive everywhere),
  width-aware, deterministic, and idempotent — properties enforced by fuzzing.

Fork of https://github.com/joyme123/thrift-ls, parser + lexer + formatter rewritten, lsp overhauled

## Installation

```bash
go install github.com/karitham/thrift-ls@latest
```

This installs the `thrift-ls` binary. It speaks LSP over stdio and doubles as
a CLI formatter.

Prebuilt binaries (`linux`/`darwin`/`windows`, `amd64`/`arm64`) and a VS Code
extension (`thrift-ls-<version>.vsix`) are attached to
[GitHub releases](https://github.com/karitham/thrift-ls/releases).

`nix run github:karitham/thrift-ls` runs the flake package.

## Usage

```
thrift-ls [flags]            run the language server (default)
thrift-ls lsp [flags]        run the language server
thrift-ls format [flags] <file>   format a thrift file
thrift-ls dump [--ir] <file>      dump the parse tree and formatter IR
```

Run `thrift-ls --help` or `thrift-ls format --help` for the full flag list.

### As a language server

`thrift-ls` is a plain LSP server speaking JSON-RPC over stdio. No editor
extension is required: any LSP client can attach to the binary directly.

```bash
thrift-ls
```

or, explicitly:

```bash
thrift-ls lsp
```

#### helix

Helix ships a thrift language definition, so only the server and the
attachment are needed in `~/.config/helix/languages.toml`:

```toml
[language-server.thrift-ls]
command = "thrift-ls"

[[language]]
name = "thrift"
language-servers = ["thrift-ls"]
# optional: format on save via the LSP
auto-format = true
```

`thrift-ls` must be on `PATH`, or use an absolute path as `command`. The
server logs to `$TMPDIR/thrift-ls.log`; raise verbosity with `-logLevel`.

#### neovim

[nvim-lspconfig](https://github.com/neovim/nvim-lspconfig) ships a `thriftls`
entry, named after the upstream project this is a fork of. Point it at the
`thrift-ls` binary:

```lua
require("lspconfig").thriftls.setup({ cmd = { "thrift-ls" } })
```

`MasonInstall thriftls` and nixvim's `thriftls` package install the upstream
server, not this one — prefer `go install`, a release binary, or the flake.

#### vim

Use `thrift-ls` as the LSP provider for thrift files:

```vim
let g:lsp_settings = { 'thrift': { 'cmd': ['thrift-ls'] } }
```

#### vscode

Install the `thrift-ls-<version>.vsix` from the
[releases page](https://github.com/karitham/thrift-ls/releases), or run the
extension from source (`vscode/`). The extension is a thin LSP client: it
finds `thrift-ls` on `PATH` (or via the `thrift-ls.path` setting), and offers
to download the matching release binary on first use. Formatting — whole
document, selection, and on-type — works through the server, so format-on-save
needs no extra setup.

### As a formatter

```bash
# print the formatted file to stdout
thrift-ls format path/to/file.thrift

# overwrite the file in place
thrift-ls format -w path/to/file.thrift

# print a diff instead
thrift-ls format -d path/to/file.thrift

# batch-format a tree
find . -name "*.thrift" | xargs -n 1 thrift-ls format -w
```

Formatting flags:

| Flag                   | Meaning                                                                                            |
| ---------------------- | -------------------------------------------------------------------------------------------------- |
| `-w`                   | Overwrite the file with the formatted result                                                       |
| `-d`                   | Print a diff instead of the formatted result                                                       |
| `--printWidth`         | Target line width (default 80)                                                                     |
| `--indent`             | Indentation: a literal like `"  "` or `"\t"` |
| `--align`              | `field`, `assign`, or `disable`                                                                    |
| `--<construct>-separator` | Separators per construct (`struct`, `union`, `exception`, `enum`, `argument`, `throws`, `list`, `map`): `comma`, `semicolon`, `none`, or `preserve` (keep as written) |
| `--break-<construct>`  | Always break the construct's bodies onto multiple lines (same constructs)                          |
| `--config`             | Path to a `thrift-ls.json` config file                                                              |
| `-I`                   | Additional include path, like the thrift compiler's `-I` (repeatable)                              |

Flags override the config file.

### Debugging: `dump`

`thrift-ls dump` prints the parse tree — every token with its position,
blank-line count, and attached comment trivia, plus the node spans — which
is useful to understand how the lexer attached a comment or why the
formatter moved something:

```bash
thrift-ls dump path/to/file.thrift
```

With `--ir`, it also builds the formatter's document IR, prints it (which
records the layout decisions on the groups), and dumps the IR tree showing
which groups broke and which stayed flat:

```bash
thrift-ls dump --ir --printWidth 100 path/to/file.thrift
```

## Formatter behavior

The formatter is **lossless**: comments, `@` annotations, and blank lines
survive formatting everywhere — including comments inside container types
(`map<string, /* c */ i32>`), const values, and annotation parens. The
formatted output re-parses cleanly and formatting is idempotent and
deterministic; comment preservation, idempotency, and parseability are
enforced by a fuzzer over the full option space.

### Conditional breaking, zig-style

Like `zig fmt`, a **trailing delimiter** on the last item of a list decides
whether the list folds:

```thrift
struct S {
  1: i32 a;
  2: string b;
}
```

stays multiline because the source ends the last field with `;`, while

```thrift
struct S {
  1: i32 a
  2: string b
}
```

folds to `struct S { 1: i32 a 2: string b }` when it fits. The rule applies
to struct/union/exception bodies, enum bodies, function arguments, and
throws clauses. The `break.*` options force the multiline layout regardless
of the source.

Note: a trailing delimiter only forces the multiline layout when the
separator mode actually emits it — `remove` drops separators, so it cannot
force a break (the output would not round-trip).

### Width-aware folding

Every group — struct bodies, function signatures, argument lists, throws
clauses, const lists, annotations — decides independently whether it fits
in the remaining width at its position. In particular, arguments and throws
fold independently:

```thrift
service Processor {
  string upload(
    1: string               imageUrl,
    2: arguments.Size       size,
    3: arguments.Identifier id,
  ) throws (1: errors.ProcessingError err)
}
```

The arguments break (trailing commas), while the throws clause stays flat
because it fits on the closing paren's line. Comments or blank lines inside
a clause force it to break, without breaking the other clause.

### Column alignment

Struct/union/exception fields and enum values are column-aligned within
their group (`align: field`). Alignment groups split at blank lines and
comments, like whitespace. A group is aligned only when the padded columns
fit within `printWidth` — except layouts that were deliberately
column-aligned in the source, which are preserved even when they overflow.
Trailing comments may overflow their line without affecting alignment.

## Configuration

Configuration lives in a `thrift-ls.json` file, discovered by walking up from
the file being formatted or the workspace root (like Biome). Set the
`THRIFT_LS_CONFIG` env var to point at an explicit config file.

```json
{
  "printWidth": 100,
  "indent": "  ",
  "tabWidth": 4,
  "align": "field",
  "separators": {
    "structs": "semicolon",
    "unions": "semicolon",
    "exceptions": "semicolon",
    "enums": "comma",
    "arguments": "comma",
    "throws": "comma",
    "lists": "comma",
    "maps": "comma",
    "sets": "comma"
  },
  "break": {
    "structs": true,
    "unions": true,
    "exceptions": true,
    "enums": true,
    "lists": true,
    "maps": true,
    "sets": true
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

Indentation is a literal string of spaces or tabs, e.g. `"  "` or
`"\t"`. Default: `"    "` (four spaces).

### tabWidth

Display width of a tab when measuring line width. Default: `4`.

### align

Controls column alignment of struct/union/exception fields and enum values.

- `field`: Align field IDs, requiredness, and types (default)
- `assign`: Align the `=` sign for default values
- `disable`: No alignment

See [Column alignment](#column-alignment) for how alignment interacts with
width, comments, and blank lines.

### separators

Controls trailing separators per construct, independently. The
`separators` object has one key per construct: `structs`, `unions`,
`exceptions`, `enums`, `arguments` (function arguments), `throws`
(throws entries), `lists` and `maps` (const list and map values). Each
accepts:

- `comma`: Always add trailing commas
- `semicolon`: Always add trailing semicolons
- `none`: Remove trailing separators
- `preserve`: Keep as written (default)

For `lists` and `maps`, the separator appears between the items and after
the last item; `none` removes them entirely (`[1, 2]` becomes `[1 2]`).
For example, semicolons in structs and commas in enums:

```json
"separators": {
  "structs": "semicolon",
  "unions": "semicolon",
  "exceptions": "semicolon",
  "enums": "comma"
}
```

Broken (multiline) argument and throws blocks are column-aligned like
struct fields, controlled by `align`.

The separator mode also interacts with [conditional
breaking](#conditional-breaking-zig-style): a mode that keeps or adds
trailing separators (`preserve`, `comma`, `semicolon`) lets a source
trailing delimiter force the multiline layout, while `none` always folds
when the group fits. Under `preserve`, a *mixed* separator pattern (some
fields separated, some not) also forces the multiline layout — a flat line
whose separators are inconsistently present looks broken.

### break

Forces layouts that would otherwise collapse to one line to stay
multiline, regardless of the source's trailing delimiters. Like
`separators`, the `break` object has one key per construct: `structs`,
`unions`, `exceptions`, `enums`, `arguments`, `throws`, `lists`, `maps`.

All default to `false`.

### includePaths

List of additional paths to search for included thrift files. When a thrift
file uses `include "foo.thrift"`, thrift-ls first tries to resolve it relative
to the current file's directory. If not found, it searches each path in
`includePaths` in order. This is similar to Apache Thrift's `-I` flag.

### logLevel

Controls logging verbosity (the server logs to `$TMPDIR/thrift-ls.log`):

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

The formatter is fuzz-tested end to end: `FuzzFormat` checks that any clean
document formats without errors, keeps every comment, is idempotent and
deterministic across the whole option space. The lexer, parser, doc
printer, LSP offset mapper, and range formatting each have their own fuzz
targets; the corpus entries under `testdata/fuzz` are permanent regression
tests.

`thrift-ls dump` (see above) is the debugging companion: it shows the parse
tree and the formatter's document IR with the layout decisions, so a
formatting issue can be pinned to the parser, the IR construction, or the
printer.
