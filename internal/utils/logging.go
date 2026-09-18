package utils

import (
	"log"
	"os"

	"github.com/akshatsinha007/kubuto/internal/config"
	"github.com/go-logr/logr"
	"github.com/go-logr/stdr"
)

// NewLogger creates a new logger based on the provided configuration and command-line flags.
func NewLogger(cfg *config.LoggingConfig, verbose bool) logr.Logger {
	var verbosity int
	if verbose {
		verbosity = 1
	} else {
		// Default to info level if not set
		level := cfg.Level
		if level == "" {
			level = "info"
		}

		// Set verbosity level from config
		switch level {
		case "debug":
			verbosity = 1
		default:
			verbosity = 0
		}
	}
	stdr.SetVerbosity(verbosity)

	// For now, we only support text format to stderr.
	// JSON and file output can be added later.
	return stdr.New(log.New(os.Stderr, "", log.LstdFlags))
}
