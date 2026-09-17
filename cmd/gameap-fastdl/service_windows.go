package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/gameap/gameap-fastdl/internal/app"
	"golang.org/x/sys/windows/svc"
)

func terminationSignal() os.Signal {
	return os.Interrupt
}

func service(filename string) error {
	return svc.Run("gameap-fastdl", &windowsService{
		filename: filename,
	})
}

type windowsService struct {
	filename string
}

func (s *windowsService) Execute(
	_ []string,
	requests <-chan svc.ChangeRequest,
	changes chan<- svc.Status,
) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- app.Run(ctx, s.filename, ready)
	}()

	status := svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}

	select {
	case err := <-done:
		if err != nil {
			slog.Error("FastDL service failed to start", "error", err)

			return true, 1
		}

		return false, 0
	case <-ready:
		changes <- status
	}

	for {
		select {
		case err := <-done:
			if err != nil {
				slog.Error("FastDL service failed", "error", err)

				return true, 1
			}

			return false, 0
		case request := <-requests:
			switch request.Cmd {
			case svc.Interrogate:
				changes <- status
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()

				if err := <-done; err != nil {
					return true, 1
				}

				return false, 0
			}
		}
	}
}
