package control

import (
	"fmt"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

// TransportFactory builds a client transport for daemon requests.
type TransportFactory func(local transport.Peer, handler transport.MsgHandler) (transport.Transport, error)

// StoreFactory opens the unit store on demand.
type StoreFactory func() (storage.Storage, error)

// Controller executes systemctl commands against the SCM and the daemon.
type Controller struct {
	manager   scm.Manager
	transport TransportFactory
	store     StoreFactory
}

func New(manager scm.Manager, transport TransportFactory, store StoreFactory) *Controller {
	return &Controller{manager: manager, transport: transport, store: store}
}

// Enable creates or updates the SCM service for a unit and returns non-fatal
// warnings (ignored directives, invalid paths).
func (c *Controller) Enable(id unit.ID, executable string, password, passwordFile string, passwordStdin bool) ([]string, error) {
	if err := ensureNotManager(id.Name); err != nil {
		return nil, err
	}

	store, err := c.store()
	if err != nil {
		return nil, err
	}
	data, err := store.ReadUnit(id.String())
	if err != nil {
		return nil, err
	}
	parsed, err := unit.Parse(id, data)
	if err != nil {
		return nil, err
	}

	warnings := append([]string{}, parsed.Warnings...)
	warnings = append(warnings, unitPathWarnings(parsed)...)

	resolvedPassword, err := resolvePassword(password, passwordFile, passwordStdin)
	if err != nil {
		return warnings, err
	}

	cfg := scm.NewServiceConfig(parsed, executable, scm.StartTypeAutomatic)
	cfg.SetPassword(resolvedPassword)

	if _, err := c.manager.QueryStatus(id.Name); err != nil {
		return warnings, c.manager.CreateService(cfg)
	}
	return warnings, c.manager.UpdateService(cfg)
}

func (c *Controller) Disable(id unit.ID) error {
	if err := ensureNotManager(id.Name); err != nil {
		return err
	}
	if _, err := c.manager.QueryStatus(id.Name); err != nil {
		return nil
	}
	if err := c.manager.StopService(id.Name); err != nil {
		return err
	}
	if err := waitStopped(c.manager, id.Name, 2*time.Minute); err != nil {
		return err
	}
	return c.manager.DeleteService(id.Name)
}

func (c *Controller) Start(id unit.ID) error {
	if err := ensureNotManager(id.Name); err != nil {
		return err
	}
	return c.manager.StartService(id.Name)
}

func (c *Controller) Stop(id unit.ID) error {
	if err := ensureNotManager(id.Name); err != nil {
		return err
	}
	return c.manager.StopService(id.Name)
}

func (c *Controller) Restart(id unit.ID) error {
	if err := ensureNotManager(id.Name); err != nil {
		return err
	}
	if err := c.manager.StopService(id.Name); err != nil {
		return err
	}
	if err := waitStopped(c.manager, id.Name, 2*time.Minute); err != nil {
		return err
	}
	return c.manager.StartService(id.Name)
}

func ensureNotManager(name string) error {
	if name == config.ManagerServiceName {
		return fmt.Errorf("service %s is the vsystemd manager and cannot be managed by systemctl", name)
	}
	return nil
}
