package main

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chowkidar/internal/config"
)

type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarn
	levelError
)

var (
	configuredLogLevel = levelInfo
)

func initLogging() {
	configuredLogLevel = parseLogLevel(os.Getenv("LOG_LEVEL"))
}

func parseLogLevel(raw string) logLevel {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return levelDebug
	case "warn", "warning":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

func logLevelLabel(level logLevel) string {
	switch level {
	case levelDebug:
		return "DEBUG"
	case levelWarn:
		return "WARN"
	case levelError:
		return "ERROR"
	default:
		return "INFO"
	}
}

func shouldLog(level logLevel) bool {
	return level >= configuredLogLevel
}

func logAt(level logLevel, format string, args ...any) {
	if !shouldLog(level) {
		return
	}
	log.Printf("[%s] "+format, append([]any{logLevelLabel(level)}, args...)...)
}

func logDebugf(format string, args ...any) {
	logAt(levelDebug, format, args...)
}

func logInfof(format string, args ...any) {
	logAt(levelInfo, format, args...)
}

func logWarnf(format string, args ...any) {
	logAt(levelWarn, format, args...)
}

func logErrorf(format string, args ...any) {
	logAt(levelError, format, args...)
}

// rotatingLogWriter tees log output to stdout and a daily-rotated file under dir.
type rotatingLogWriter struct {
	mu          sync.Mutex
	dir         string
	currentDate string
	file        *os.File
}

var globalLogWriter *rotatingLogWriter

func (w *rotatingLogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, _ = os.Stdout.Write(p)
	today := time.Now().Format("2006-01-02")
	if w.file == nil || w.currentDate != today {
		if w.file != nil {
			_ = w.file.Close()
			w.file = nil
		}
		path := filepath.Join(w.dir, "app-"+today+".log")
		if f, ferr := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); ferr == nil {
			w.file = f
			w.currentDate = today
		}
	}
	if w.file != nil {
		_, _ = w.file.Write(p)
	}
	return len(p), nil
}

func (w *rotatingLogWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
}

func pruneOldLogs(logsDir string, retentionDays int) {
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "app-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		dateStr := strings.TrimPrefix(strings.TrimSuffix(name, ".log"), "app-")
		t, terr := time.Parse("2006-01-02", dateStr)
		if terr != nil {
			continue
		}
		if t.Before(cutoff) {
			if err := os.Remove(filepath.Join(logsDir, name)); err != nil {
				logWarnf("logs: failed to prune file=%s err=%v", name, err)
			} else {
				logInfof("logs: pruned file=%s", name)
			}
		}
	}
}

func applyLogConfig(logsDir string, cfg config.Config) {
	level := cfg.LogLevel
	if level == "" {
		level = "info" // safe default if neither env var nor config.yaml specifies a level
	}
	configuredLogLevel = parseLogLevel(level)
	if cfg.LogToFile {
		if globalLogWriter == nil {
			globalLogWriter = &rotatingLogWriter{dir: logsDir}
		} else {
			globalLogWriter.mu.Lock()
			globalLogWriter.dir = logsDir
			globalLogWriter.mu.Unlock()
		}
		log.SetOutput(globalLogWriter)
		if cfg.LogRetentionDays > 0 {
			pruneOldLogs(logsDir, cfg.LogRetentionDays)
		}
	} else {
		log.SetOutput(os.Stdout)
		if globalLogWriter != nil {
			globalLogWriter.close()
			globalLogWriter = nil
		}
	}
}
