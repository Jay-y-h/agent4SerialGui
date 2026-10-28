package service

import (
	"agent4SerialGui/internal/config"
	"agent4SerialGui/internal/gateway"
	"agent4SerialGui/internal/protocol"
	"agent4SerialGui/internal/serialport"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type device struct {
	cfg    config.DeviceConfig
	serial *serialport.Manager
	log    *slog.Logger
}

type Agent struct {
	gw      *gateway.Client
	devices map[string]*device // 按设备 id 索引，用于网关消息路由
	order   []*device          // 保持配置顺序
	log     *slog.Logger
}

func newAgent(cfg *config.Config) (*Agent, error) {
   base:=slog.Default()

   gw,err:=gateway.New(cfg.Server,cfg.Server.AgentID,base)
   if err!=nil{
	return nil,err
   }
   a:=&Agent{gw: gw,devices: make(map[string]*device,len(cfg.Devices)),log: base}
   for _,dc:=range cfg.Devices{
	l:=base.With("device",dc.ID)
	d:=&device{cfg: dc,serial:serialport.New(dc.Serial,l),log: l}
	a.devices[dc.ID]=d
	a.order=append(a.order, d)
   }
   return a,nil
}

func Run(ctx context.Context, cfg *config.Config) error {
	a, err := newAgent(cfg)
	if err != nil {
		return fmt.Errorf("初始化失败: %w", err)
	}

	var wg sync.WaitGroup

	// 启动本地信息服务。
	infoServer := NewInfoServer(cfg.Server.AgentID)
	wg.Add(1)
	go func() {
		defer wg.Done()

		if err := infoServer.Run(ctx); err != nil {
			a.log.Error("信息服务退出", "error", err)
		}
	}()

	// 启动串口设备。
	for _, d := range a.order {
		d := d
		d.log.Info("设备启动", "name", d.cfg.Name, "serial", d.cfg.Serial.Port)

		wg.Add(1)
		go func() {
			defer wg.Done()
			d.serial.Run(ctx, func(frame []byte) {
				a.onSerialFrame(d, frame)
			})
		}()
	}

	wg.Wait()
	return nil
}

// 网关 -> 串口（按 to 路由到设备）
func (a *Agent) onGatewayMessage(raw []byte) {
	var msg protocol.Message
	if err:=json.Unmarshal(raw,&msg);err!=nil{
		a.log.Error("网关消息解析失败", "error", err, "raw", string(raw))
		return
	}
	if msg.Type != protocol.TypeBrowser {
		a.log.Warn("不是串口写入消息", "type", msg.Type)
		return
	}
	//这里传入的id，就是需要向此id设备发串口数据
	d, err := a.route(msg.ID)
	if err != nil {
		a.log.Warn("无法路由串口写入消息", "error", err, "id", msg.ID, "to", msg.To)
		return
	}
	data := msg.Payload.Data
	if len(data) == 0 {
		d.log.Warn("串口写入消息 data 为空", "id", msg.ID)
		return
	}
	if err := d.serial.Write(data); err != nil {
		d.log.Error("写串口失败", "error", err, "id", msg.ID)
		return
	}
	d.log.Info("网关 -> 串口", "id", msg.ID, "len", len(data), "hex", hex.EncodeToString(data))


}
// 串口 -> 网关（from 标明是哪个设备）。多个设备的 goroutine 会并发调用，Send 内部已串行化写入。
func (a *Agent) onSerialFrame(d *device, frame []byte) {
	msg:=protocol.Message{
		ID: d.cfg.ID,
		Type: d.cfg.Name,
		From: d.cfg.ID,
		Timestamp: time.Now().UnixMilli(),
		Payload: protocol.SerialPayload{Data: frame},
	}
	b,err:=json.Marshal(msg)
	if err!=nil{
		d.log.Error("JSON 编码失败", "error", err)
		return
	}
	if err := a.gw.Send(b); err != nil {
		d.log.Error("串口数据转发网关失败", "error", err, "len", len(frame), "hex", hex.EncodeToString(frame))
		return
	}
	d.log.Info("串口 -> 网关", "len", len(frame), "hex", hex.EncodeToString(frame))

}

// route 按 to 找设备；to 为空时，仅当只有一个设备才可省略（兼容单设备旧协议）。
func (a *Agent) route(to string) (*device, error) {
	if to != "" {
		if d, ok := a.devices[to]; ok {
			return d, nil
		}
		return nil, fmt.Errorf("未知设备 %q", to)
	}
	if len(a.order) == 1 {
		return a.order[0], nil
	}
	return nil, fmt.Errorf("配置了多个设备，消息必须带 to 字段")
}