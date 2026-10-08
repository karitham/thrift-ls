# JetBrains plugin

The native LSP plugin supports GoLand and RubyMine 2026.2 or newer. It connects
to the `thrift-ls` executable over stdio. Install the server binary separately;
it is not included in the plugin ZIP.

## Build and test from source

Install **JDK 21** and **Gradle 9 or newer** (CI uses Gradle 9.8). From the
repository root, build and test once against GoLand, then verify the packaged
plugin against both GoLand and RubyMine:

```sh
gradle -p jetbrains -Pide=goland test buildPlugin verifyPlugin
```

`-Pide=goland` is the default if omitted. Use `-Pide=rubymine` to run tests or
launch the development sandbox on RubyMine instead. `test` runs the
executable-path and `*.thrift` file-association tests; `buildPlugin` creates
`jetbrains/build/distributions/thrift-ls-jetbrains-<version>.zip`;
`verifyPlugin` checks binary compatibility with **both** IDEs. The
repository's [CI and release workflows](../.github/workflows/) run this single
build/test job and package a ZIP for releases. To run one test class, for
example:

```sh
gradle -p jetbrains -Pide=goland test --tests 'com.karitham.thriftls.ThriftFileTypeTest'
```

Test reports are written to `jetbrains/build/reports/tests/test/`.

## Run in an IDE

For a development sandbox, use `gradle -p jetbrains -Pide=goland runIde` (or
`-Pide=rubymine`). Alternatively, install the local ZIP or one from the
[releases page](https://github.com/karitham/thrift-ls/releases) using
**Settings → Plugins → Install Plugin from Disk**. Install a `thrift-ls`
executable separately, then open a `.thrift` file in a project.

The plugin searches the IDE process's `PATH` for `thrift-ls`. To use a
different executable (or if the IDE cannot find it), set its **absolute path**
under **Settings → Tools → Thrift Language Server**. Changing the setting
restarts servers for open projects. Open a directory containing `.thrift` files
as the IDE project.

The plugin associates `*.thrift` with the **Thrift LS** file type in
**Settings → Editor → File Types**. If another plugin already owns `*.thrift`,
choose Thrift LS there to make this plugin the default file type. The language
server still attaches by file extension if you choose another file type, but
claiming the extension as Thrift LS can replace highlighting from a TextMate
bundle or another Thrift plugin. LSP semantic highlighting is provided by the
server when it runs.

For a manual smoke test, check diagnostics, completion, go-to-definition, and
formatting in an open `.thrift` file. After changing `thrift-ls.json`, restart
the server using the IDE's Language Services status widget to reload its
settings. For protocol troubleshooting, enable `#com.intellij.platform.lsp`
in **Help → Diagnostic Tools → Debug Log Settings** and inspect the IDE log.
