package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

type InfoServer struct {
	server *http.Server
	ln     net.Listener
}

// 本地信息服务的监听地址与允许访问的页面来源（测试里会改 infoAddr）。
var infoAddr = "127.0.0.1:15124"

const allowOrigin = "https://app.local"

func NewInfoServer(agentID string) *InfoServer {
	mux := http.NewServeMux()

	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowOrigin)
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"agentId": agentID,
		})
	})

	return &InfoServer{
		server: &http.Server{
			Addr:              infoAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Listen 同步占用端口。端口被占用时立即返回错误，
// 让调用方（代理启动流程）能把它当成启动失败，而不是在后台默默失败。
func (s *InfoServer) Listen() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return fmt.Errorf("info server 监听 %s 失败: %w", s.server.Addr, err)
	}
	s.ln = ln
	return nil
}

// Serve 在 Listen 成功后提供服务，ctx 结束时优雅关闭（最多等 3 秒）。
func (s *InfoServer) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := s.server.Shutdown(sctx); err != nil {
			_ = s.server.Close()
		}
	}()

	err := s.server.Serve(s.ln)
	if err == http.ErrServerClosed {
		return nil
	}
	return fmt.Errorf("info server: %w", err)
}
