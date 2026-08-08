# thrift-ls VS Code extension

Language server and formatter for Apache Thrift IDL files, powered by the
[thrift-ls](https://github.com/karitham/thrift-ls) binary. The server itself
implements formatting (whole document, range, and on-type), completion,
diagnostics, definition, references, rename, document symbols, folding, and
semantic tokens, so the extension is a thin LSP client.

## Install

The extension does not bundle a binary. On first use it looks for `thrift-ls`
in the following order:

1. the `thrift-ls.path` setting (absolute path), or
2. `thrift-ls` on `PATH`, or
3. a previously downloaded copy in the extension's storage.

If none is found, a prompt offers to download the release binary for your
platform (`linux`/`darwin`/`windows`, `amd64`/`arm64`) and verify its
SHA-256 against `checksums.txt` from the release.

A stable vsix downloads from `releases/latest` (never a prerelease). A dev
vsix from a per-commit prerelease (`0.1.0-dev.<sha>`) pins itself to that
same commit's release, so the extension and the server binary always come
from the same code.

Manual installs:

```bash
go install github.com/karitham/thrift-ls@latest
```

or grab a binary from the [releases page](https://github.com/karitham/thrift-ls/releases)
and put it on `PATH` (or point `thrift-ls.path` at it).

## Features

- Formatting (format on save, format selection, format on type)
- Completion, go to definition, find references, rename, hover
- Diagnostics, document symbols, folding, semantic tokens

All server features are negotiated over LSP; no editor-specific code beyond the
client. The `thrift-ls.downloadServer` command re-downloads the binary (use it
to update): it stops the running server first — required on Windows, where a
running executable cannot be replaced — and restarts the client on the new
binary. `thrift-ls.openReleases` opens the releases page.

## Settings

The formatter options (`printWidth`, `indent`, `tabWidth`, `align`,
`separators.*`, `break.*`) are exposed as `thrift-ls.*` settings. They are
sent to the server on startup and re-sent via `didChangeConfiguration` when
they change, so formatting picks up new values without a restart. Settings
override the `thrift-ls.json` config file — discovered from the workspace
root, one config per folder — and the CLI flags passed to the server are
applied underneath the settings.

`includePaths` and `logLevel` are not exposed as settings — use
`thrift-ls.json` or the `-I` / `-logLevel` flags when launching the server.
Server logs go to the "Thrift Language Server" output channel (via
`window/logMessage`) and to `$TMPDIR/thrift-ls.log`.

## Development

```bash
npm install
npm run compile     # typecheck + build out/
npm run watch       # incremental
npm run package     # build the .vsix
```

Run the extension in VS Code with the Extension Development Host
(`F5` / `Debug: Start Debugging` from the Run view) after opening this folder.

Syntax highlighting is not included; install a thrift grammar extension if you
want colors.
