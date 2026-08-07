// Package log configures the process-wide slog logger for thrift-ls.
//
// thrift-ls logs to a file in the temp directory (thriftls.log) so that LSP
// traffic on stdio is never polluted with log output.
package log

import (
	"log/slog"
	"os"
)

// Init configures the default slog logger with the given level and redirects
// it to the temp log file.
//
// level uses the historical thrift-ls scale (1 fatal .. 6 trace), matching
// the old logrus levels so CLI flags keep their meaning.
func Init(level int) {
	file := os.TempDir() + "/thriftls.log"

	logFile, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o766)
	if err != nil {
		panic(err)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{
		Level: slogLevel(level),
	})))
}

func slogLevel(level int) slog.Level {
	switch {
	case level >= 5: // logrus Debug / Trace
		return slog.LevelDebug
	case level == 4: // logrus Info
		return slog.LevelInfo
	case level == 3: // logrus Warn
		return slog.LevelWarn
	default: // logrus Fatal / Error / Panic
		return slog.LevelError
	}
}
