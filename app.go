package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"agent4SerialGui/internal/config"
	"agent4SerialGui/internal/logger"
	"agent4SerialGui/internal/service"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	statusStopped = "stopped"
	statusRunning = "running"
	statusError   = "error" // 代理自己异常退出（如信息服务端口被占用）

	maxLogLines = 500
	stopTimeout = 8 * time.Second

	eventState = "agent:state" // 载荷: AppState
	eventLog   = "agent:log"   // 载荷: string（一行日志）
)

type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type DeviceState struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Port  string `json:"port"`
	State string `json:"state"` // open / closed
}

type AppState struct {
	ActiveMode   string        `json:"activeMode"`
	Status       string        `json:"status"`
	Error        string        `json:"error"`
	AgentID      string        `json:"agentId"`
	Gateway      string        `json:"gateway"`      // 网关地址
	GatewayState string        `json:"gatewayState"` // connected / disconnected
	Devices      []DeviceState `json:"devices"`
}

type App struct {
	ctx     context.Context // Wails 上下文，用于发事件
	baseDir string          // config.json 与相对日志目录所在的基准目录

	startMu sync.Mutex // 串行化 StartAgent，防止并发双击导致重复启动

	mu         sync.Mutex
	activeMode string
	status     string
	lastErr    string
	agentID    string
	gateway    string
	gwState    string
	devices    []DeviceState
	cancel     context.CancelFunc
	done       chan struct{}

	logMu sync.Mutex // 单独的锁：日志回调可能发生在持有 mu 的代码路径里
	logs  []string
}

func NewApp() *App {
	return &App{
		baseDir:    resolveBaseDir(),
		activeMode: "mode-1",
		status:     statusStopped,
		gwState:    "disconnected",
	}
}

// resolveBaseDir 优先使用可执行文件所在目录（要求其下有 config.json），
// 否则退回当前工作目录（wails dev 时即项目根目录）。
func resolveBaseDir() string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		if _, err := os.Stat(filepath.Join(dir, "config.json")); err == nil {
			return dir
		}
	}
	wd, _ := os.Getwd()
	return wd
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// shutdown 在窗口关闭时由 Wails 调用，确保串口和连接被正常释放。
func (a *App) shutdown(ctx context.Context) {
	_, _ = a.StopAgent()
}

// ---- 模式（来自 config.json 的 modes）----------------------------------

func (a *App) loadConfig() (*config.File, error) {
	return config.Load(filepath.Join(a.baseDir, "config.json"))
}

// GetModes 返回 config.json 里定义的模式；配置有误时返回错误，界面会显示出来。
func (a *App) GetModes() ([]Mode, error) {
	file, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	modes := make([]Mode, 0, len(file.Modes))
	for _, m := range file.Modes {
		ports := make([]string, 0, len(m.Devices))
		for _, d := range m.Devices {
			ports = append(ports, d.Serial.Port)
		}
		modes = append(modes, Mode{ID: m.ID, Name: m.Name, Description: m.Server.AgentID + " · " + strings.Join(ports, ", ")})
	}
	return modes, nil
}

func (a *App) GetState() AppState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateLocked()
}

func (a *App) stateLocked() AppState {
	devices := make([]DeviceState, len(a.devices))
	copy(devices, a.devices)
	return AppState{
		ActiveMode:   a.activeMode,
		Status:       a.status,
		Error:        a.lastErr,
		AgentID:      a.agentID,
		Gateway:      a.gateway,
		GatewayState: a.gwState,
		Devices:      devices,
	}
}

// emitState 把当前状态推给前端（调用时不能持有 a.mu 以外的锁，也不要持有 a.mu）。
func (a *App) emitState() {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, eventState, a.GetState())
}

func (a *App) SelectMode(id string) (AppState, error) {
	a.mu.Lock()
	running := a.status == statusRunning
	a.mu.Unlock()
	if running {
		return a.GetState(), fmt.Errorf("请先停止代理，再切换模式")
	}

	file, err := a.loadConfig()
	if err != nil {
		return a.GetState(), err
	}
	if _, err := file.Find(id); err != nil {
		return a.GetState(), err
	}

	a.mu.Lock()
	a.activeMode = id
	a.lastErr = ""
	a.mu.Unlock()
	a.emitState()
	return a.GetState(), nil
}

// ---- 启停 ---------------------------------------------------------------

func (a *App) StartAgent() (AppState, error) {
	a.startMu.Lock()
	defer a.startMu.Unlock()

	a.mu.Lock()
	if a.status == statusRunning {
		s := a.stateLocked()
		a.mu.Unlock()
		return s, nil
	}
	mode := a.activeMode
	a.mu.Unlock()

	file, err := a.loadConfig()
	if err != nil {
		return a.GetState(), fmt.Errorf("加载配置失败: %w", err)
	}
	m, err := file.Find(mode)
	if err != nil {
		return a.GetState(), fmt.Errorf("加载配置失败: %w", err)
	}
	cfg := &m.Config

	// 所有模式共用同一个日志文件；相对路径以 baseDir 为基准，避免随工作目录变化。
	logCfg := file.Log
	if !filepath.IsAbs(logCfg.File) {
		logCfg.File = filepath.Join(a.baseDir, logCfg.File)
	}
	closer, err := logger.Init(logCfg, a.appendLog)
	if err != nil {
		return a.GetState(), fmt.Errorf("初始化日志失败: %w", err)
	}
	// 每条日志都带上模式 id，方便在同一个文件里区分。
	slog.SetDefault(slog.Default().With("mode", mode))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	a.mu.Lock()
	a.cancel, a.done = cancel, done
	a.status = statusRunning
	a.lastErr = ""
	a.agentID = cfg.Server.AgentID
	a.gateway = cfg.Server.URL
	a.gwState = "disconnected"
	a.devices = a.devices[:0]
	for _, d := range cfg.Devices {
		a.devices = append(a.devices, DeviceState{ID: d.ID, Name: d.Name, Port: d.Serial.Port, State: "closed"})
	}
	a.mu.Unlock()

	slog.Info("GUI 启动代理", "mode", mode)

	go func() {
		err := service.Run(ctx, cfg, service.Hooks{
			OnGateway: a.setGateway,
			OnDevice:  a.setDevice,
		})
		if err != nil {
			slog.Error("代理退出", "error", err)
		} else {
			slog.Info("代理已停止")
		}
		_ = closer.Close()
		cancel()

		a.mu.Lock()
		a.cancel = nil
		if err != nil {
			a.status, a.lastErr = statusError, err.Error()
		} else {
			a.status = statusStopped
		}
		a.gwState = "disconnected"
		for i := range a.devices {
			a.devices[i].State = "closed"
		}
		close(done)
		a.mu.Unlock()
		a.emitState()
	}()

	a.emitState()
	return a.GetState(), nil
}

func (a *App) StopAgent() (AppState, error) {
	a.mu.Lock()
	cancel, done := a.cancel, a.done
	a.mu.Unlock()

	if cancel == nil {
		return a.GetState(), nil
	}
	cancel()

	// 等串口和网关 goroutine 退出，避免切换模式时抢占串口；但不无限等待，防止界面假死。
	select {
	case <-done:
	case <-time.After(stopTimeout):
		return a.GetState(), fmt.Errorf("停止超时（%s），后台仍在退出，请稍后再试", stopTimeout)
	}
	return a.GetState(), nil
}

func (a *App) setGateway(connected bool) {
	a.mu.Lock()
	if connected {
		a.gwState = "connected"
	} else {
		a.gwState = "disconnected"
	}
	a.mu.Unlock()
	a.emitState()
}

func (a *App) setDevice(id string, open bool) {
	a.mu.Lock()
	for i := range a.devices {
		if a.devices[i].ID == id {
			if open {
				a.devices[i].State = "open"
			} else {
				a.devices[i].State = "closed"
			}
		}
	}
	a.mu.Unlock()
	a.emitState()
}

// ---- 日志：内存环形缓冲 + 事件推送 --------------------------------------

func (a *App) appendLog(line string) {
	a.logMu.Lock()
	if len(a.logs) >= maxLogLines {
		copy(a.logs, a.logs[1:])
		a.logs = a.logs[:len(a.logs)-1]
	}
	a.logs = append(a.logs, line)
	a.logMu.Unlock()

	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, eventLog, line)
	}
}

// GetLogs 返回内存里最近的日志，前端首次加载时用；之后靠 agent:log 事件增量更新。
func (a *App) GetLogs() []string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	out := make([]string, len(a.logs))
	copy(out, a.logs)
	return out
}

// ClearLogs 清空内存日志（磁盘上的日志文件不受影响）。
func (a *App) ClearLogs() {
	a.logMu.Lock()
	a.logs = nil
	a.logMu.Unlock()
}
