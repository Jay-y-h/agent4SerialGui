package logger

import (
	"agent4SerialGui/internal/config"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Init 创建日志目录与文件，并把 slog 默认 logger 设为同时输出到文件和终端。
func Init(cfg config.LogConfig) error {
	if err := os.MkdirAll(cfg.Path, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(cfg.Path, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	level := slog.LevelInfo
	var parsed slog.Level
	if parsed.UnmarshalText([]byte(cfg.Level)) == nil { // 支持 debug/info/warn/error
		level = parsed
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(
		io.MultiWriter(f, os.Stdout),
		&slog.HandlerOptions{Level: level},
	)))
	return nil
}
