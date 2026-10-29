package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"agent4SerialGui/internal/config"
)

// Init 创建日志文件（及其所在目录），并把 slog 默认 logger 指向该文件。
// hook 不为空时，每条日志（已去掉换行）都会回调一次，供界面实时显示。
// 返回的 Closer 必须在代理停止后调用：它会关闭文件并恢复之前的默认 logger。
func Init(cfg config.LogConfig, hook func(line string)) (io.Closer, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.File), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}

	level := slog.LevelInfo
	var parsed slog.Level
	if parsed.UnmarshalText([]byte(cfg.Level)) == nil { // 支持 debug/info/warn/error
		level = parsed
	}

	w := &hookWriter{f: f, hook: hook}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})))

	return closerFunc(func() error {
		slog.SetDefault(prev)
		return w.Close()
	}), nil
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// hookWriter 写文件，并把每行日志交给 hook。
type hookWriter struct {
	mu   sync.Mutex
	f    *os.File
	hook func(string)
}

func (w *hookWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f == nil {
		return 0, os.ErrClosed
	}
	n, err := w.f.Write(p)
	if w.hook != nil {
		w.hook(strings.TrimRight(string(p), "\r\n"))
	}
	return n, err
}

func (w *hookWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
