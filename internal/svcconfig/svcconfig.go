package svcconfig

import (
	"github.com/gwg605/vsystemd/internal/scm"
	"github.com/gwg605/vsystemd/internal/unit"
)

// FromUnit builds an SCM service configuration from a service unit. When
// content is non-empty, its hash is embedded in the command line so the runner
// can verify the applied snapshot it loads.
func FromUnit(file *unit.UnitFile, binaryPath string, startType scm.StartType, content unit.Content) scm.ServiceConfig {
	binary := `"` + binaryPath + `" --mode=run --unit=` + file.Name.String()
	if len(content) > 0 {
		binary += ` --snapshot=` + content.Hash()
	}
	return scm.ServiceConfig{
		Name:         file.Name.Name,
		BinaryPath:   binary,
		StartType:    startType,
		Dependencies: file.Dependencies(),
		User:         scm.NewAccount(file.Service.User, file.Name.Name),
		Description:  file.Description,
	}
}
