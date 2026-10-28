package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"agent4SerialGui/internal/config"
	"agent4SerialGui/internal/logger"
	"agent4SerialGui/internal/service"
)

type Mode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AppState struct {
	ActiveMode string   `json:"activeMode"`
	Status     string   `json:"status"`
	AgentID    string   `json:"agentId"`
	Gateway    string   `json:"gateway"`
	Device     string   `json:"device"`
	Logs       []string `json:"logs"`
}

type App struct {
	mu         sync.Mutex
	activeMode string
	status     string
	agentID    string
	gateway    string
	device     string
	logPath    string
	cancel     context.CancelFunc
	done       chan struct{}
}

var modes = []Mode{
	{ID: "mode-1", Name: "模式一", Description: "配置一"},
	{ID: "mode-2", Name: "模式二", Description: "配置二"},
	{ID: "mode-3", Name: "模式三", Description: "配置三"},
	{ID: "mode-4", Name: "模式四", Description: "配置四"},
}

func NewApp() *App {
	return &App{
		activeMode: "mode-1",
		status:     "stopped",
	}
}

func (a *App) startup(ctx context.Context) {
	// Wails 启动时调用，暂不启动串口代理。
}

func (a *App) GetModes() []Mode {
	return modes
}

func (a *App) GetState() AppState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stateLocked()
}

func (a *App) stateLocked() AppState {
	return AppState{
		ActiveMode: a.activeMode,
		Status:     a.status,
		AgentID:    a.agentID,
		Gateway:    a.gateway,
		Device:     a.device,
		Logs:       readLogTail(a.logPath, 500),
	}
}

func (a *App) SelectMode(id string) (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status == "running" {
		return a.stateLocked(), fmt.Errorf("请先停止代理，再切换模式")
	}

	found := false
	for _, mode := range modes {
		if mode.ID == id {
			found = true
			break
		}
	}
	if !found {
		return a.stateLocked(), fmt.Errorf("未知模式: %s", id)
	}

	a.activeMode = id
	return a.stateLocked(), nil
}

func (a *App) StartAgent() (AppState, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.status == "running" {
		return a.stateLocked(), nil
	}

	configPath := filepath.Join("configs", a.activeMode+".json")
	cfg, err := config.Load(configPath)
	if err != nil {
		return a.stateLocked(), fmt.Errorf("加载配置失败: %w", err)
	}

	// 将日志目录转成绝对路径，避免相对路径因工作目录变化而失效。
	logDir, err := filepath.Abs(cfg.Log.Path)
	if err != nil {
		return a.stateLocked(), fmt.Errorf("解析日志目录失败: %w", err)
	}
	cfg.Log.Path = logDir

	if err := logger.Init(cfg.Log); err != nil {
		return a.stateLocked(), fmt.Errorf("初始化日志失败: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	a.cancel = cancel
	a.done = done
	a.status = "running"
	a.agentID = cfg.Server.AgentID
	a.gateway = cfg.Server.URL
	a.logPath = filepath.Join(cfg.Log.Path, "agent.log")

	var ports []string
	for _, d := range cfg.Devices {
		ports = append(ports, d.Serial.Port)
	}
	a.device = strings.Join(ports, ", ")

	slog.Info("GUI 启动代理", "mode", a.activeMode)

	go func() {
		err := service.Run(ctx, cfg)
		if err != nil {
			slog.Error("代理退出", "error", err)
		}

		a.mu.Lock()
		a.status = "stopped"
		a.cancel = nil
		close(done)
		a.mu.Unlock()
	}()

	return a.stateLocked(), nil
}

func (a *App) StopAgent() (AppState, error) {
	a.mu.Lock()
	cancel := a.cancel
	done := a.done

	if cancel == nil {
		state := a.stateLocked()
		a.mu.Unlock()
		return state, nil
	}

	cancel()
	a.mu.Unlock()

	// 等待串口和网关 goroutine 退出，避免切换模式时抢占串口。
	<-done

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cancel = nil
	a.status = "stopped"
	return a.stateLocked(), nil
}

func readLogTail(path string, limit int) []string {
	if path == "" {
		return []string{}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return []string{}
	}

	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	if len(lines) == 1 && lines[0] == "" {
		return []string{}
	}
	return lines
}
