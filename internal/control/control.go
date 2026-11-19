package control

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/gwg605/vsystemd/internal/config"
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/transport"
	"github.com/gwg605/vsystemd/internal/unit"
)

// Controller executes systemctl commands against the SCM and the daemon.
type Controller struct {
	manager   scm.Manager
	transport transport.Factory
	store     storage.Factory
}

func New(manager scm.Manager, transport transport.Factory, store storage.Factory) *Controller {
	return &Controller{manager: manager, transport: transport, store: store}
}

// Enable creates or updates the SCM service for a unit and returns non-fatal
// warnings (ignored directives, invalid paths). A missing unit file is allowed
// only when the service already exists: the start type is set to automatic.
func (c *Controller) Enable(id unit.ID, executable string, password, passwordFile string, passwordStdin bool) ([]string, error) {
	if err := ensureNotManager(id.Name); err != nil {
		return nil, err
	}

	exists, err := c.serviceExists(id.Name)
	if err != nil {
		return nil, err
	}

	store, err := c.store()
	if err != nil {
		return nil, err
	}
	parsed, warnings, err := loadUnitFile(store, id)
	if err != nil {
		return warnings, err
	}

	if parsed == nil {
		if !exists {
			return warnings, fmt.Errorf("unit %s not found", id)
		}
		return warnings, c.manager.SetStartType(id.Name, scm.StartTypeAutomatic)
	}

	resolvedPassword, err := resolvePassword(password, passwordFile, passwordStdin)
	if err != nil {
		return warnings, err
	}
	cfg := scm.NewServiceConfig(parsed, executable, scm.StartTypeAutomatic)
	cfg.SetPassword(resolvedPassword)

	if !exists {
		return warnings, c.manager.CreateService(cfg)
	}
	return warnings, c.manager.UpdateService(cfg)
}

func (c *Controller) Disable(id unit.ID) error {
	if err := ensureNotManager(id.Name); err != nil {
		return err
	}
	exists, err := c.serviceExists(id.Name)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return c.manager.SetStartType(id.Name, scm.StartTypeDisabled)
}

func (c *Controller) serviceExists(name string) (bool, error) {
	_, err := c.manager.QueryStatus(name)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, scm.ErrNotFound):
		return false, nil
	default:
		return false, err
	}
}

func loadUnitFile(store storage.Storage, id unit.ID) (*unit.UnitFile, []string, error) {
	data, err := store.ReadUnit(id.String())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	parsed, err := unit.Parse(id, data)
	if err != nil {
		return nil, nil, err
	}
	warnings := append([]string{}, parsed.Warnings...)
	warnings = append(warnings, unitPathWarnings(store, parsed)...)
	return parsed, warnings, nil
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
