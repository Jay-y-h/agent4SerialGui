package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type InfoServer struct {
	server *http.Server
}

func NewInfoServer(agentID string) *InfoServer {
	mux := http.NewServeMux()

	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		// w.Header().Set("Access-Control-Allow-Origin", "https://app.dev")
		w.Header().Set("Access-Control-Allow-Origin", "https://app.local")
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
			Addr:    "127.0.0.1:15124",
			Handler: mux,
			ReadHeaderTimeout: 5*time.Second,
		},
	}
}

func (s *InfoServer) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()

		_ = s.server.Shutdown(context.Background())
	}()

	err := s.server.ListenAndServe()

	if err == http.ErrServerClosed {
		return nil
	}

	return fmt.Errorf("info server: %w", err)
}
