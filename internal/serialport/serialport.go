package serialport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"agent4SerialGui/internal/config"

	"go.bug.st/serial"
)

const (
	retryDelay    = 3 * time.Second
	maxRetryDelay = 30 * time.Second
	maxFrame      = 4096 // 单帧上限，防止设备持续吐数据导致缓存无限增长
)

type Manager struct {
	cfg     config.SerialConfig
	log     *slog.Logger
	onState func(open bool) // 串口打开/断开时回调，可为 nil
	mu      sync.Mutex      // 保护 port，Write 与重连会并发访问
	port    serial.Port
}

func New(cfg config.SerialConfig, log *slog.Logger, onState func(open bool)) *Manager {
	return &Manager{cfg: cfg, log: log, onState: onState}
}

func (m *Manager) notify(open bool) {
	if m.onState != nil {
		m.onState(open)
	}
}

// Run 负责打开串口、分帧、异常后自动重开，直到 ctx 结束。
// 每读到完整的一帧就调用 handle（handle 拿到的切片归它所有）。
// 启动时串口不存在也不会退出，会按退避间隔一直重试到设备接入；
// 同一种错误只在第一次出现时打 WARN，避免日志刷屏。
func (m *Manager) Run(ctx context.Context, handle func([]byte)) {
	delay := retryDelay
	lastErr := ""

	for ctx.Err() == nil {
		p, err := m.open()
		if err == nil {
			delay, lastErr = retryDelay, ""
			m.setPort(p)
			m.notify(true)
			m.log.Info("串口已打开", "port", m.cfg.Port)

			err = readFrames(ctx, p, handle)

			m.setPort(nil)
			m.notify(false)
			_ = p.Close()
			if ctx.Err() != nil {
				m.log.Info("串口已关闭")
				return
			}
			if err == nil {
				err = errors.New("串口读取中断")
			}
		}

		if msg := err.Error(); msg != lastErr {
			m.log.Warn("串口不可用，稍后重试", "error", err, "retry_in", delay)
			lastErr = msg
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, maxRetryDelay)
	}
}

func (m *Manager) open() (serial.Port, error) {
	mode := &serial.Mode{BaudRate: m.cfg.BaudRate, DataBits: m.cfg.DataBits}

	switch m.cfg.Parity {
	case "E":
		mode.Parity = serial.EvenParity
	case "O":
		mode.Parity = serial.OddParity
	default:
		mode.Parity = serial.NoParity
	}
	if m.cfg.StopBits == 2 {
		mode.StopBits = serial.TwoStopBits
	}

	p, err := serial.Open(m.cfg.Port, mode)
	if err != nil {
		return nil, fmt.Errorf("打开串口 %s 失败: %w", m.cfg.Port, err)
	}

	// 读超时 = 分帧静默间隔：超时且已有缓存 => 一帧结束。
	// 同时保证 Read 不会永久阻塞，ctx 取消能及时生效。
	if err := p.SetReadTimeout(m.cfg.FrameGap()); err != nil {
		_ = p.Close()
		return nil, fmt.Errorf("设置读超时失败: %w", err)
	}
	return p, nil
}

func (m *Manager) setPort(p serial.Port) {
	m.mu.Lock()
	m.port = p
	m.mu.Unlock()
}

// readFrames 从 r 读数据并按"静默间隔"切帧：r 的 Read 每次最多阻塞一个静默间隔，
// 返回 0 字节即代表静默了，此时把已缓存的数据作为一帧发出。
func readFrames(ctx context.Context, r io.Reader, handle func([]byte)) error {
	buf := make([]byte, 1024)
	var frame []byte

	flush := func() {
		if len(frame) > 0 {
			handle(frame)
		}
		frame = nil
	}
	defer flush()

	for ctx.Err() == nil {
		n, err := r.Read(buf)
		if n > 0 {
			frame = append(frame, buf[:n]...)
			if len(frame) > maxFrame {
				flush()
			}
		}
		if err != nil {
			return err
		}
		if n == 0 {
			flush()
		}
	}
	return nil
}

func (m *Manager) Write(data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.port == nil {
		return errors.New("串口未打开")
	}
	_, err := m.port.Write(data)
	return err
}
