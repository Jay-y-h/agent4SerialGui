package serialport

import (
	"agent4SerialGui/internal/config"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.bug.st/serial"
)

const (
	retryDelay = 3 * time.Second
	maxFrame   = 4096 // 单帧上限，防止设备持续吐数据导致缓存无限增长
)

type Manager struct {
	cfg  config.SerialConfig
	log  *slog.Logger
	mu   sync.Mutex // 保护 port，Write 与重连会并发访问
	port serial.Port
}

func New(cfg config.SerialConfig, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, log: log}
}

// Run 负责打开串口、按静默间隔分帧、异常后自动重开，直到 ctx 结束。
// 每读到完整的一帧就调用 handle（handle 拿到的切片归它所有）。
// 启动时串口不存在也不会退出，会一直重试到设备接入。
func (m *Manager) Run(ctx context.Context, handle func([]byte)) {
	for ctx.Err()==nil{
		p,err:=m.open()
		if err==nil{
			m.setPort(p)
			m.log.Info("串口已打开", "port", m.cfg.Port)

			err=m.readFrames(ctx,p,handle)

			m.setPort(nil)
			_=p.Close()
			if ctx.Err()!=nil{
				m.log.Info("串口已关闭")
				return
			}
		}

		m.log.Warn("串口不可用，稍后重试", "error", err, "retry_in", retryDelay)
		select {
			case <-ctx.Done():
				return
			case <-time.After(retryDelay):
		}

	}
}

func (m *Manager) open() (serial.Port, error) {
	mode:=&serial.Mode{BaudRate: m.cfg.BaudRate,DataBits: m.cfg.DataBits}

	switch m.cfg.Parity{
		case "E":
			mode.Parity = serial.EvenParity
		case "O":
			mode.Parity = serial.OddParity
		default:
			mode.Parity = serial.NoParity
	}
	if m.cfg.StopBits==2{
		mode.StopBits=serial.TwoStopBits
	}

	p,err:=serial.Open(m.cfg.Port,mode)
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

// readFrames 不再需要额外的 goroutine / channel / timer：
// Read 每次最多阻塞一个静默间隔，返回 0 字节即代表"静默了"。
func (m *Manager) readFrames(ctx context.Context, p serial.Port, handle func([]byte)) error {
	buf := make([]byte, 1024)
	var frame []byte

	flush:=func(){
		if len(frame)>0{
			handle(frame)
			frame=nil
		}
	}
	defer flush()

	for ctx.Err()==nil{
		n,err:=p.Read(buf)
		if err!=nil{
			return err
		}

		if n==0{
			flush()
			continue
		}
		frame = append(frame, buf[:n]...)
		if len(frame)>maxFrame {
			flush()
		}
	}
	return  nil

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