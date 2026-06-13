package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	EnvLog      = "ICLOUD_CLI_LOG"
	EnvLogLevel = "ICLOUD_CLI_LOG_LEVEL"
	EnvLogFile  = "ICLOUD_CLI_LOG_FILE"
	EnvLogSize  = "ICLOUD_CLI_LOG_SIZE"
	EnvLogNum   = "ICLOUD_CLI_LOG_NUM"

	DestinationFile   = "file"
	DestinationStderr = "stderr"
	DestinationOff    = "off"

	defaultLevel   = "warn"
	defaultSizeMB  = 10
	defaultHistory = 3
)

type Config struct {
	Destination string   `json:"destination"`
	Level       string   `json:"level"`
	FilePath    string   `json:"file_path,omitempty"`
	SizeMB      int      `json:"size_mb"`
	History     int      `json:"history"`
	Warnings    []string `json:"warnings,omitempty"`
}

type FileStatus struct {
	Exists    bool  `json:"exists"`
	SizeBytes int64 `json:"size_bytes"`
}

type Status struct {
	Enabled          bool        `json:"enabled"`
	Destination      string      `json:"destination"`
	Level            string      `json:"level"`
	FilePath         string      `json:"file_path,omitempty"`
	SizeMB           int         `json:"size_mb"`
	History          int         `json:"history"`
	ActiveFile       *FileStatus `json:"active_file,omitempty"`
	RotatedFileCount int         `json:"rotated_file_count,omitempty"`
	Warnings         []string    `json:"warnings,omitempty"`
}

var (
	mu     sync.RWMutex
	active = slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg    = Config{Destination: DestinationOff, Level: defaultLevel, SizeMB: defaultSizeMB, History: defaultHistory}
)

func LoadConfig() Config {
	out := Config{
		Destination: DestinationFile,
		Level:       defaultLevel,
		FilePath:    defaultFilePath(),
		SizeMB:      defaultSizeMB,
		History:     defaultHistory,
	}

	if value := strings.TrimSpace(os.Getenv(EnvLog)); value != "" {
		switch strings.ToLower(value) {
		case DestinationFile, DestinationStderr, DestinationOff:
			out.Destination = strings.ToLower(value)
		default:
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s=%q is invalid; using %s", EnvLog, value, DestinationFile))
		}
	}

	if value := strings.TrimSpace(os.Getenv(EnvLogLevel)); value != "" {
		switch strings.ToLower(value) {
		case "info", "warn", "error":
			out.Level = strings.ToLower(value)
		default:
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s=%q is invalid; using %s", EnvLogLevel, value, defaultLevel))
		}
	}

	if value := strings.TrimSpace(os.Getenv(EnvLogFile)); value != "" {
		out.FilePath = value
	}

	if value := strings.TrimSpace(os.Getenv(EnvLogSize)); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s=%q is invalid; using %d", EnvLogSize, value, defaultSizeMB))
		} else {
			out.SizeMB = n
		}
	}

	if value := strings.TrimSpace(os.Getenv(EnvLogNum)); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s=%q is invalid; using %d", EnvLogNum, value, defaultHistory))
		} else {
			out.History = n
		}
	}

	return out
}

func Configure(config Config, stderr io.Writer) Config {
	level := slogLevel(config.Level)
	handlerOpts := &slog.HandlerOptions{Level: level}

	for _, warning := range config.Warnings {
		fmt.Fprintf(fallbackWriter(stderr), "icloud logging warning: %s\n", warning)
	}
	printedWarnings := len(config.Warnings)

	if config.Destination == DestinationOff {
		setLogger(slog.New(slog.NewJSONHandler(io.Discard, handlerOpts)), config)
		return config
	}

	var writer io.Writer
	switch config.Destination {
	case DestinationStderr:
		writer = fallbackWriter(stderr)
	case DestinationFile:
		w, err := newRotatingWriter(config.FilePath, int64(config.SizeMB)*1024*1024, config.History)
		if err != nil {
			config.Warnings = append(config.Warnings, fmt.Sprintf("failed to open log file %q: %v; using stderr", config.FilePath, err))
			config.Destination = DestinationStderr
			writer = fallbackWriter(stderr)
		} else {
			writer = w
		}
	default:
		config.Warnings = append(config.Warnings, fmt.Sprintf("unknown log destination %q; using file", config.Destination))
		config.Destination = DestinationFile
		w, err := newRotatingWriter(config.FilePath, int64(config.SizeMB)*1024*1024, config.History)
		if err != nil {
			config.Warnings = append(config.Warnings, fmt.Sprintf("failed to open log file %q: %v; using stderr", config.FilePath, err))
			config.Destination = DestinationStderr
			writer = fallbackWriter(stderr)
		} else {
			writer = w
		}
	}
	for _, warning := range config.Warnings[printedWarnings:] {
		fmt.Fprintf(fallbackWriter(stderr), "icloud logging warning: %s\n", warning)
	}

	setLogger(slog.New(slog.NewJSONHandler(writer, handlerOpts)), config)
	return config
}

func CurrentConfig() Config {
	mu.RLock()
	defer mu.RUnlock()
	return cfg
}

func CurrentStatus() Status {
	config := CurrentConfig()
	status := Status{
		Enabled:     config.Destination != DestinationOff,
		Destination: config.Destination,
		Level:       config.Level,
		SizeMB:      config.SizeMB,
		History:     config.History,
		Warnings:    append([]string{}, config.Warnings...),
	}
	if config.Destination == DestinationFile {
		status.FilePath = config.FilePath
		active := FileStatus{}
		if info, err := os.Stat(config.FilePath); err == nil {
			active.Exists = true
			active.SizeBytes = info.Size()
		}
		status.ActiveFile = &active
		status.RotatedFileCount = countRotated(config.FilePath, config.History)
	}
	return status
}

func Info(msg string, args ...any) {
	logWithLevel(slog.LevelInfo, msg, args...)
}

func Warn(msg string, args ...any) {
	logWithLevel(slog.LevelWarn, msg, args...)
}

func Error(msg string, args ...any) {
	logWithLevel(slog.LevelError, msg, args...)
}

func SanitizedArgs(args []string) []string {
	out := make([]string, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			out = append(out, "[redacted]")
			redactNext = false
			continue
		}
		name, hasValue := splitFlag(arg)
		switch name {
		case "--app-password", "-app-password", "--apple-id", "-apple-id":
			if hasValue {
				out = append(out, name+"=[redacted]")
			} else {
				out = append(out, arg)
				redactNext = true
			}
		case "--input-json", "-input-json":
			if hasValue {
				out = append(out, name+"=[json]")
			} else {
				out = append(out, arg)
				redactNext = true
			}
		default:
			out = append(out, redactInlineSecrets(arg))
		}
	}
	return out
}

func SanitizedIMAPCommand(line string) string {
	line = strings.TrimSpace(line)
	upper := strings.ToUpper(line)
	if strings.HasPrefix(upper, "LOGIN ") {
		return "LOGIN [redacted]"
	}
	return redactInlineSecrets(line)
}

func SanitizedSMTPCommand(line string) string {
	line = strings.TrimSpace(line)
	upper := strings.ToUpper(line)
	if strings.HasPrefix(upper, "AUTH ") {
		return "AUTH [redacted]"
	}
	return redactInlineSecrets(line)
}

func SanitizedURL(raw string) string {
	replacer := strings.NewReplacer("\n", "", "\r", "")
	raw = replacer.Replace(raw)
	if i := strings.Index(raw, "?"); i >= 0 {
		raw = raw[:i]
	}
	return raw
}

func setLogger(logger *slog.Logger, config Config) {
	mu.Lock()
	defer mu.Unlock()
	active = logger
	cfg = config
}

func logWithLevel(level slog.Level, msg string, args ...any) {
	mu.RLock()
	logger := active
	mu.RUnlock()
	logger.Log(contextWithoutCancel{}, level, msg, args...)
}

func fallbackWriter(stderr io.Writer) io.Writer {
	if stderr != nil {
		return stderr
	}
	return os.Stderr
}

func slogLevel(level string) slog.Level {
	switch strings.ToLower(level) {
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

func defaultFilePath() string {
	base, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(base) == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "icloud-cli", "icloud.log")
}

func splitFlag(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "-") {
		return arg, false
	}
	if i := strings.Index(arg, "="); i >= 0 {
		return arg[:i], true
	}
	return arg, false
}

func redactInlineSecrets(s string) string {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "app_password") || strings.Contains(lower, "app-password") || strings.Contains(lower, "authorization") {
		return "[redacted]"
	}
	return s
}

func countRotated(path string, history int) int {
	count := 0
	for i := 1; i <= history; i++ {
		if _, err := os.Stat(fmt.Sprintf("%s.%d", path, i)); err == nil {
			count++
		}
	}
	return count
}

type contextWithoutCancel struct{}

func (contextWithoutCancel) Deadline() (time.Time, bool) { return time.Time{}, false }
func (contextWithoutCancel) Done() <-chan struct{}       { return nil }
func (contextWithoutCancel) Err() error                  { return nil }
func (contextWithoutCancel) Value(any) any               { return nil }
