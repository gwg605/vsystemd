//go:build windows

package daemon

import "golang.org/x/sys/windows/svc"

type ServiceHandler struct {
	daemon *Daemon
}

func NewServiceHandler(daemon *Daemon) *ServiceHandler {
	return &ServiceHandler{daemon: daemon}
}

func RunService(name string, daemon *Daemon) error {
	return svc.Run(name, NewServiceHandler(daemon))
}

func (h *ServiceHandler) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	defer h.daemon.Close()

	changes <- svc.Status{State: svc.StartPending}
	if err := h.daemon.Start(); err != nil {
		changes <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	running := svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	changes <- running
	for request := range requests {
		switch request.Cmd {
		case svc.Interrogate:
			changes <- running
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		}
	}
	return false, 0
}
