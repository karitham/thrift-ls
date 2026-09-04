package analyzers

import (
	"github.com/karitham/thrift-ls/sema"
)

var _ func(sema.Config) *sema.Pipeline = DefaultPipeline
