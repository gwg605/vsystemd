//go:build windows

package daemon

import (
	"log/slog"
	"runtime/debug"

	"golang.org/x/sys/windows/svc"
)

type ServiceHandler struct {
	daemon *Daemon
}

func NewServiceHandler(daemon *Daemon) *ServiceHandler {
	return &ServiceHandler{daemon: daemon}
}

func RunService(name string, daemon *Daemon) error {
	return svc.Run(name, NewServiceHandler(daemon))
}

func (h *ServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("daemon: panic in service handler", "panic", recovered, "stack", string(debug.Stack()))
			ssec, errno = false, 1
		}
	}()
	defer h.daemon.Close()

	slog.Info("daemon: service start pending")
	changes <- svc.Status{State: svc.StartPending}
	if err := h.daemon.Start(); err != nil {
		slog.Error("daemon: start failed", "error", err)
		changes <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	running := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	changes <- running
	slog.Info("daemon: service running")
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			slog.Debug("daemon: interrogate")
			changes <- running
		case svc.Stop, svc.Shutdown:
			slog.Info("daemon: service stop requested", "control", request.Cmd)
			changes <- svc.Status{State: svc.StopPending}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
	return false, 0
}
