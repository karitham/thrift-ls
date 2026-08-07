package lsp

const (
	ServerName       = "thrift-ls"
	LanguageIDThrift = "thrift"
)

// ServerVersion is the version reported by --version and the initialize
// handshake. It defaults to "dev" and is replaced at release build time
// with the git tag:
//
//	go build -ldflags "-X github.com/karitham/thrift-ls/lsp.ServerVersion=0.1.0"
var ServerVersion = "dev"
