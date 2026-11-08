package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"time"
)

//go:embed display/index.html
var displayAssets embed.FS

const displayServerAddress = "127.0.0.1:8769"

func newDisplayHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"service": "my-livehub-display",
		})
	})

	mux.HandleFunc("/display/validation", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		content, err := fs.ReadFile(displayAssets, "display/index.html")
		if err != nil {
			http.Error(w, "display page unavailable", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(content))
	})

	return mux
}

func startDisplayServer() (*http.Server, error) {
	listener, err := net.Listen("tcp", displayServerAddress)
	if err != nil {
		return nil, err
	}

	server := &http.Server{
		Handler:           newDisplayHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("OBS display server stopped unexpectedly: %v", err)
		}
	}()

	return server, nil
}
