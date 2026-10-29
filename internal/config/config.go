package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

// File 对应整个 config.json：一份全局日志配置 + 若干模式。
type File struct {
	Log   LogConfig `json:"log"`
	Modes []Mode    `json:"modes"`
}

// Mode 是 modes 数组的一项：一个模式 = id + 名称 + 一份完整配置，
// 不同模式之间互不影响。
type Mode struct {
	ID   string `json:"id"`
	Name string `json:"name"` // 界面显示名，省略则用 id
	Config
}

// Config 是一个模式运行时需要的全部配置。
type Config struct {
	Server  ServerConfig   `json:"server"`
	Devices []DeviceConfig `json:"devices"`
}

type ServerConfig struct {
	URL               string `json:"url"`
	AgentID           string `json:"agent_id"`
	Token             string `json:"token"`              // 可选；非空时以 Authorization: Bearer 发给网关
	ReconnectInterval int    `json:"reconnect_interval"` // 秒，重连退避的起点
	HeartbeatInterval int    `json:"heartbeat_interval"` // 秒
}

type DeviceConfig struct {
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Serial SerialConfig `json:"serial"`
}

type SerialConfig struct {
	Port     string `json:"port"`
	BaudRate int    `json:"baud_rate"`
	DataBits int    `json:"data_bits"`
	StopBits int    `json:"stop_bits"`
	Parity   string `json:"parity"` // N / E / O
	// ReadTimeout 毫秒。既是串口读超时，也是分帧的"静默间隔"：
	// 连续 ReadTimeout 毫秒没有新数据，就认为一帧结束。
	ReadTimeout int `json:"read_timeout"`
}

// LogConfig 全局只有一份：所有模式写同一个日志文件（每行带 mode=模式id）。
type LogConfig struct {
	Level string `json:"level"` // debug / info / warn / error，默认 info
	File  string `json:"file"`  // 日志文件路径，默认 ./logs/agent.log
}

func (s ServerConfig) ReconnectDelay() time.Duration {
	return time.Duration(s.ReconnectInterval) * time.Second
}

func (s ServerConfig) Heartbeat() time.Duration {
	return time.Duration(s.HeartbeatInterval) * time.Second
}

func (s SerialConfig) FrameGap() time.Duration {
	return time.Duration(s.ReadTimeout) * time.Millisecond
}

// UnmarshalJSON 让每个模式先带上默认值，配置里只需要写与默认值不同的字段。
func (m *Mode) UnmarshalJSON(b []byte) error {
	type plain Mode
	p := plain{Config: Config{
		Server: ServerConfig{ReconnectInterval: 5, HeartbeatInterval: 30},
	}}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*m = Mode(p)
	return nil
}

// UnmarshalJSON 让每个设备的 serial 先带上默认值（9600/8/1/N，50ms 静默分帧），
// 配置里只需要写 port、baud_rate 等与默认值不同的字段。
func (d *DeviceConfig) UnmarshalJSON(b []byte) error {
	type plain DeviceConfig
	p := plain{Serial: SerialConfig{BaudRate: 9600, DataBits: 8, StopBits: 1, Parity: "N", ReadTimeout: 50}}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*d = DeviceConfig(p)
	return nil
}

// Load 读取并校验 config.json。
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f := &File{Log: LogConfig{Level: "info", File: "./logs/agent.log"}}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}

	switch strings.ToLower(f.Log.Level) {
	case "debug", "info", "warn", "error":
	default:
		return nil, fmt.Errorf("配置无效: log.level 只支持 debug/info/warn/error: %q", f.Log.Level)
	}
	if len(f.Modes) == 0 {
		return nil, fmt.Errorf("配置无效: modes 至少配置一个模式")
	}

	seen := make(map[string]bool)
	for i := range f.Modes {
		m := &f.Modes[i]
		if m.ID == "" {
			return nil, fmt.Errorf("配置无效: modes[%d].id 不能为空", i)
		}
		if seen[m.ID] {
			return nil, fmt.Errorf("配置无效: 模式 id 重复: %q", m.ID)
		}
		seen[m.ID] = true

		if m.Name == "" {
			m.Name = m.ID
		}
		for j := range m.Devices {
			m.Devices[j].Serial.Parity = strings.ToUpper(m.Devices[j].Serial.Parity)
		}
		if err := m.Config.validate(); err != nil {
			return nil, fmt.Errorf("配置无效: 模式 %s: %w", m.ID, err)
		}
	}
	return f, nil
}

// Find 按 id 查找模式。
func (f *File) Find(id string) (*Mode, error) {
	for i := range f.Modes {
		if f.Modes[i].ID == id {
			return &f.Modes[i], nil
		}
	}
	return nil, fmt.Errorf("未知模式: %s", id)
}

func (c *Config) validate() error {
	if u, err := url.Parse(c.Server.URL); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return fmt.Errorf("server.url 必须是 ws:// 或 wss:// 地址: %q", c.Server.URL)
	}
	if c.Server.AgentID == "" {
		return fmt.Errorf("server.agent_id 不能为空")
	}
	if c.Server.ReconnectInterval <= 0 || c.Server.HeartbeatInterval <= 0 {
		return fmt.Errorf("server.reconnect_interval / heartbeat_interval 必须大于 0")
	}
	if len(c.Devices) == 0 {
		return fmt.Errorf("devices 至少配置一个设备")
	}

	ids := make(map[string]bool)
	ports := make(map[string]string) // 大写端口名 -> 设备 id（COM4 与 com4 视为同一个）
	for i, d := range c.Devices {
		if d.ID == "" {
			return fmt.Errorf("devices[%d].id 不能为空", i)
		}
		if ids[d.ID] {
			return fmt.Errorf("设备 id 重复: %q", d.ID)
		}
		ids[d.ID] = true

		if err := d.Serial.validate(); err != nil {
			return fmt.Errorf("设备 %s: %w", d.ID, err)
		}
		key := strings.ToUpper(d.Serial.Port)
		if other, dup := ports[key]; dup {
			return fmt.Errorf("串口 %s 被设备 %s 和 %s 同时使用", d.Serial.Port, other, d.ID)
		}
		ports[key] = d.ID
	}
	return nil
}

func (s SerialConfig) validate() error {
	switch {
	case s.Port == "":
		return fmt.Errorf("serial.port 不能为空")
	case s.BaudRate <= 0 || s.ReadTimeout <= 0:
		return fmt.Errorf("serial.baud_rate / read_timeout 必须大于 0")
	case s.DataBits < 5 || s.DataBits > 8:
		return fmt.Errorf("serial.data_bits 只支持 5-8: %d", s.DataBits)
	case s.StopBits != 1 && s.StopBits != 2:
		return fmt.Errorf("serial.stop_bits 只支持 1 或 2: %d", s.StopBits)
	case s.Parity != "N" && s.Parity != "E" && s.Parity != "O":
		return fmt.Errorf("serial.parity 只支持 N/E/O: %q", s.Parity)
	}
	return nil
}
