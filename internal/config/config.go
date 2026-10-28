package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"
)

type Config struct {
	Server  ServerConfig   `json:"server"`
	Log     LogConfig      `json:"log"`
	Devices []DeviceConfig `json:"devices"`
}

type ServerConfig struct {
	URL               string `json:"url"`
	AgentID           string `json:"agent_id"`
	Reconnect         bool   `json:"reconnect"`
	ReconnectInterval int    `json:"reconnect_interval"`
	HeartbeatInterval int    `json:"heartbeat_interval"`
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

type LogConfig struct {
	Level string `json:"level"` // debug / info / warn / error
	Path  string `json:"path"`
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

func defaultSerial() SerialConfig {
	return SerialConfig{BaudRate: 9600, DataBits: 8, StopBits: 1, Parity: "N", ReadTimeout: 50}
}


// UnmarshalJSON 让每个设备的 serial 都先带上默认值，
// 配置里只需要写与默认值不同的字段（比如只写 port 和 baud_rate）。
func (d *DeviceConfig) UnmarshalJSON(b []byte) error {
	type plain DeviceConfig
	p := plain{Serial: defaultSerial()}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*d = DeviceConfig(p)
	return nil
}


// Load 读取配置：先填默认值，再用文件内容覆盖，最后校验。
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Server: ServerConfig{Reconnect: true, ReconnectInterval: 5, HeartbeatInterval: 30},
		Log:    LogConfig{Level: "info", Path: "./logs"},
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}

	if cfg.Server.AgentID == "" {
		if h, err := os.Hostname(); err == nil && h != "" {
			cfg.Server.AgentID = h
		} else {
			cfg.Server.AgentID = "serial-agent"
		}
	}
	for i := range cfg.Devices {
		cfg.Devices[i].Serial.Parity = strings.ToUpper(cfg.Devices[i].Serial.Parity)
	}
	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("配置无效: %w", err)
	}
	return cfg, nil
}


func (c *Config) validate() error {
	if u, err := url.Parse(c.Server.URL); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return fmt.Errorf("server.url 必须是 ws:// 或 wss:// 地址: %q", c.Server.URL)
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
