package app

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gameap/gameap-fastdl/internal/config"
	"github.com/gameap/gameap-fastdl/internal/server"
)

func Run(ctx context.Context, filename string) error {
	cfg, err := config.Load(filename)
	if err != nil {
		return err
	}

	handler, err := server.New(cfg)
	if err != nil {
		return err
	}
	defer handler.Close()

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}
	defer listener.Close()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go handler.Watch(ctx, cfg.ServersDir)

	httpServer := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16384,
	}

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// The shutdown must outlive the canceled run context.
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()

			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				_ = httpServer.Close()
			}
		case <-done:
		}
	}()
	defer close(done)
	slog.Info("FastDL listening", "address", cfg.Listen)

	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return err
}
