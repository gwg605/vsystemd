package svcconfig

import (
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

// FromUnit builds an SCM service configuration from a service unit.
func FromUnit(file *unit.UnitFile, binaryPath string, startType scm.StartType) scm.ServiceConfig {
	return scm.ServiceConfig{
		Name:         file.Name.Name,
		BinaryPath:   `"` + binaryPath + `" --mode=run --unit=` + file.Name.String(),
		StartType:    startType,
		Dependencies: file.Dependencies(),
		User:         scm.NewAccount(file.Service.User, file.Name.Name),
		Description:  file.Description,
	}
}
