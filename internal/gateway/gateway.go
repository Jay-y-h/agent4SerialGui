package gateway

import (
	"agent4SerialGui/internal/config"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const writeWait = 5 * time.Second

type Client struct {
	cfg  config.ServerConfig
	url  string
	log  *slog.Logger
	mu   sync.Mutex // 保护 conn 指针，同时串行化写入（gorilla 不允许并发写）
	conn *websocket.Conn
}

func New(cfg config.ServerConfig,agentID string,log *slog.Logger)(*Client,error){
 	u,err:=url.Parse(cfg.URL)
	if err!=nil{
		return nil, fmt.Errorf("解析 websocket URL 失败: %w", err)
	}
	q:=u.Query()
	q.Set("type","device")
	q.Set("id",agentID)
	u.RawQuery=q.Encode()

	return &Client{cfg:cfg,url:u.String(),log:log},nil
}

func (c *Client) Run(ctx context.Context, handle func([]byte)) {
	for ctx.Err()==nil{
		conn,_,err:=websocket.DefaultDialer.DialContext(ctx,c.url,nil)
		if err!=nil{
			c.log.Error("Websocket 连接失败", "error", err)
		}else{
			c.log.Info("Websocket 连接成功", "url", c.url)
			c.setConn(conn)
			err = c.serve(ctx,conn,handle)
			c.setConn(nil)
			_ = conn.Close()

			if ctx.Err()!=nil{
				c.log.Info("Websocket 已关闭")
				return
			}
			c.log.Warn("Websocket 断开", "error", err)
		}

		if !c.cfg.Reconnect {
			c.log.Warn("未开启重连，Agent 将不再接收网关消息")
			return
		}
		select{
		case <-ctx.Done():return
		case <-time.After(c.cfg.ReconnectDelay()):
		}
	}
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn=conn
	c.mu.Unlock()
}

func (c *Client) serve(ctx context.Context, conn *websocket.Conn, handle func([]byte)) error {
	heartbeat:=c.cfg.Heartbeat()
	extend:=func() error {return conn.SetReadDeadline(time.Now().Add(2*heartbeat))}
	_=extend()
	conn.SetPongHandler(func(string) error {return extend()})

	done:=make(chan struct{})
	defer close(done)
	go func ()  {
		t:=time.NewTicker(heartbeat)
		defer t.Stop()
		for{
			select{
			case <-done:return 
			case <-ctx.Done():
				_=conn.WriteControl(websocket.CloseMessage,websocket.FormatCloseMessage(websocket.CloseNormalClosure,""),time.Now().Add(time.Second))
				_=conn.Close()
				return
			case <-t.C:
				if err:=conn.WriteControl(websocket.PingMessage,nil,time.Now().Add(writeWait));err!=nil{
					_=conn.Close()
					return 
				} 
			}
			
		}
	}()

	for{
		_,data,err:=conn.ReadMessage()
		if err!=nil{
			return  err
		}
		_=extend()
		handle(data)
	}

}

func (c *Client) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn==nil{
		return errors.New("websocket 未连接")
	}

	_=c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(websocket.TextMessage,data)
}