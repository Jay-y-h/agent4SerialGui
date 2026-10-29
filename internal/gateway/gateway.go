package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"sync"
	"time"

	"agent4SerialGui/internal/config"

	"github.com/gorilla/websocket"
)

const (
	writeWait        = 5 * time.Second
	handshakeTimeout = 10 * time.Second
	maxReconnectWait = 60 * time.Second
	maxMessageSize   = 1 << 20 // 网关单条消息上限 1MB
)

// ErrNotConnected 表示当前没有可用的 WebSocket 连接。
var ErrNotConnected = errors.New("websocket 未连接")

type Client struct {
	cfg     config.ServerConfig
	url     string
	log     *slog.Logger
	onState func(connected bool) // 连接建立/断开时回调，可为 nil

	mu   sync.Mutex // 保护 conn 指针，同时串行化写入（gorilla 不允许并发写）
	conn *websocket.Conn
}

func New(cfg config.ServerConfig, agentID string, log *slog.Logger, onState func(bool)) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("解析 websocket URL 失败: %w", err)
	}
	q := u.Query()
	q.Set("type", "device")
	q.Set("id", agentID)
	u.RawQuery = q.Encode()

	return &Client{cfg: cfg, url: u.String(), log: log, onState: onState}, nil
}

func (c *Client) dial(ctx context.Context) (*websocket.Conn, error) {
	d := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: handshakeTimeout,
	}
	header := http.Header{}
	if c.cfg.Token != "" {
		header.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	conn, _, err := d.DialContext(ctx, c.url, header)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxMessageSize)
	return conn, nil
}

// Run 维持到网关的连接并把收到的消息交给 handle，直到 ctx 结束。
// 断线后按指数退避（带抖动）重连，连上一次后退避重置。
func (c *Client) Run(ctx context.Context, handle func([]byte)) {
	delay := c.cfg.ReconnectDelay()

	for ctx.Err() == nil {
		conn, err := c.dial(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.log.Error("Websocket 连接失败", "error", err, "retry_in", delay)
		} else {
			c.log.Info("Websocket 连接成功", "url", c.url)
			delay = c.cfg.ReconnectDelay()
			c.setConn(conn)
			err = c.serve(ctx, conn, handle)
			c.setConn(nil)
			_ = conn.Close()

			if ctx.Err() != nil {
				c.log.Info("Websocket 已关闭")
				return
			}
			c.log.Warn("Websocket 断开", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay + rand.N(delay/5+1)):
		}
		delay = min(delay*2, maxReconnectWait)
	}
}

func (c *Client) setConn(conn *websocket.Conn) {
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	if c.onState != nil {
		c.onState(conn != nil)
	}
}

func (c *Client) serve(ctx context.Context, conn *websocket.Conn, handle func([]byte)) error {
	heartbeat := c.cfg.Heartbeat()
	extend := func() error { return conn.SetReadDeadline(time.Now().Add(2 * heartbeat)) }
	_ = extend()
	conn.SetPongHandler(func(string) error { return extend() })

	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(heartbeat)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				_ = conn.WriteControl(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
				_ = conn.Close()
				return
			case <-t.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
					_ = conn.Close()
					return
				}
			}
		}
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		_ = extend()
		handle(data)
	}
}

// Send 发送一条文本消息；未连接时返回 ErrNotConnected。
func (c *Client) Send(data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return ErrNotConnected
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}
