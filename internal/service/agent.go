package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"agent4SerialGui/internal/config"
	"agent4SerialGui/internal/gateway"
	"agent4SerialGui/internal/protocol"
	"agent4SerialGui/internal/serialport"
)

const (
	// 每个设备最多缓存多少帧待发网关。网关断线期间超出后丢弃最旧的帧。
	outQueueSize = 256
	// 一帧在队列里等待网关恢复的最长时间，超过就丢弃（过期数据继续发意义不大）。
	maxPendingAge = 5 * time.Minute
	// 网关不可用时重试发送的间隔。
	deliverRetry = 500 * time.Millisecond
	// 网关单次下发写入串口的最大字节数。
	maxWrite = 1024
)

// Hooks 是状态变化回调，供界面展示；任一字段可为 nil。
type Hooks struct {
	OnGateway func(connected bool)
	OnDevice  func(deviceID string, open bool)
}

type queued struct {
	payload []byte // 已编码好的 JSON 消息
	frame   []byte // 原始串口数据，仅用于 debug 日志
	at      time.Time
}

type device struct {
	cfg    config.DeviceConfig
	serial *serialport.Manager
	log    *slog.Logger
	out    chan queued
}

type Agent struct {
	gw      *gateway.Client
	devices map[string]*device // 按设备 id 索引，用于网关消息路由
	order   []*device          // 保持配置顺序
	log     *slog.Logger
}

func newAgent(cfg *config.Config, hooks Hooks) (*Agent, error) {
	base := slog.Default()

	gw, err := gateway.New(cfg.Server, cfg.Server.AgentID, base, hooks.OnGateway)
	if err != nil {
		return nil, err
	}
	a := &Agent{gw: gw, devices: make(map[string]*device, len(cfg.Devices)), log: base}
	for _, dc := range cfg.Devices {
		id := dc.ID
		l := base.With("device", id)
		onState := func(open bool) {
			if hooks.OnDevice != nil {
				hooks.OnDevice(id, open)
			}
		}
		d := &device{
			cfg:    dc,
			serial: serialport.New(dc.Serial, l, onState),
			log:    l,
			out:    make(chan queued, outQueueSize),
		}
		a.devices[dc.ID] = d
		a.order = append(a.order, d)
	}
	return a, nil
}

// Run 启动信息服务、网关连接和所有串口设备，直到 ctx 结束。
// 信息服务端口被占用等启动期错误会直接返回，不会留下半启动状态。
func Run(ctx context.Context, cfg *config.Config, hooks Hooks) error {
	a, err := newAgent(cfg, hooks)
	if err != nil {
		return fmt.Errorf("初始化失败: %w", err)
	}

	info := NewInfoServer(cfg.Server.AgentID)
	if err := info.Listen(); err != nil {
		return err
	}

	var wg sync.WaitGroup
	spawn := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}

	spawn(func() {
		if err := info.Serve(ctx); err != nil {
			a.log.Error("信息服务退出", "error", err)
		}
	})

	// 网关连接：之前漏掉了这一步，串口数据发不出去、网关指令也收不到。
	spawn(func() { a.gw.Run(ctx, a.onGatewayMessage) })

	for _, d := range a.order {
		d := d
		d.log.Info("设备启动", "name", d.cfg.Name, "serial", d.cfg.Serial.Port)

		spawn(func() { d.serial.Run(ctx, func(frame []byte) { a.onSerialFrame(d, frame) }) })
		spawn(func() { a.sendLoop(ctx, d) })
	}

	wg.Wait()
	return nil
}

// 网关 -> 串口（按 to 路由到设备）
func (a *Agent) onGatewayMessage(raw []byte) {
	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		a.log.Error("网关消息解析失败", "error", err, "len", len(raw))
		return
	}
	if msg.Type != protocol.TypeBrowser {
		a.log.Warn("不是串口写入消息", "type", msg.Type)
		return
	}
	d, err := a.route(msg)
	if err != nil {
		a.log.Warn("无法路由串口写入消息", "error", err, "id", msg.ID, "to", msg.To)
		return
	}
	data := msg.Payload.Data
	switch {
	case len(data) == 0:
		d.log.Warn("串口写入消息 data 为空", "id", msg.ID)
		return
	case len(data) > maxWrite:
		d.log.Warn("写入数据过长，已拒绝", "id", msg.ID, "len", len(data), "max", maxWrite)
		return
	}
	if err := d.serial.Write(data); err != nil {
		d.log.Error("写串口失败", "error", err, "id", msg.ID)
		return
	}
	// 写入指令会改变仪器状态，保留完整 hex 便于审计。
	d.log.Info("网关 -> 串口", "id", msg.ID, "len", len(data), "hex", hex.EncodeToString(data))
}

// 串口 -> 网关。运行在串口读 goroutine 里，所以这里只编码并入队，绝不能阻塞在网络上。
func (a *Agent) onSerialFrame(d *device, frame []byte) {
	msg := protocol.Message{
		ID:        d.cfg.ID,
		Type:      d.cfg.Name,
		From:      d.cfg.ID,
		Timestamp: time.Now().UnixMilli(),
		Payload:   protocol.SerialPayload{Data: frame},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		d.log.Error("JSON 编码失败", "error", err)
		return
	}
	d.enqueue(queued{payload: b, frame: frame, at: time.Now()})
}

// enqueue 非阻塞入队；队列满时丢弃最旧的一帧，保证读串口永不被卡住。
func (d *device) enqueue(q queued) {
	for {
		select {
		case d.out <- q:
			return
		default:
		}
		select {
		case <-d.out:
			d.log.Warn("网关发送队列已满，丢弃最旧一帧", "queue", outQueueSize)
		default:
		}
	}
}

// sendLoop 按顺序把队列里的帧发给网关；网关不可用时原地等待并重试，
// 因此断线期间的数据会在重连后按原顺序补发。
func (a *Agent) sendLoop(ctx context.Context, d *device) {
	for {
		select {
		case <-ctx.Done():
			return
		case q := <-d.out:
			a.deliver(ctx, d, q)
		}
	}
}

func (a *Agent) deliver(ctx context.Context, d *device, q queued) {
	warned := false
	for {
		err := a.gw.Send(q.payload)
		if err == nil {
			d.log.Info("串口 -> 网关", "len", len(q.frame))
			d.log.Debug("串口数据", "hex", hex.EncodeToString(q.frame))
			return
		}
		if time.Since(q.at) > maxPendingAge {
			d.log.Warn("帧等待网关超时，已丢弃", "error", err, "len", len(q.frame), "age", time.Since(q.at).Round(time.Second))
			return
		}
		if !warned {
			d.log.Warn("网关不可用，帧已缓存待重发", "error", err, "len", len(q.frame))
			warned = true
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(deliverRetry):
		}
	}
}

// route 找到目标设备：
//  1. 优先按 to 字段；
//  2. 兼容旧协议：to 为空时，若 id 恰好是某个设备 id 就用它；
//  3. 再兜底：只配置了一个设备时可全部省略。
func (a *Agent) route(msg protocol.Message) (*device, error) {
	if msg.To != "" {
		if d, ok := a.devices[msg.To]; ok {
			return d, nil
		}
		return nil, fmt.Errorf("未知设备 %q", msg.To)
	}
	if d, ok := a.devices[msg.ID]; ok {
		return d, nil
	}
	if len(a.order) == 1 {
		return a.order[0], nil
	}
	return nil, fmt.Errorf("配置了多个设备，消息必须带 to 字段")
}
