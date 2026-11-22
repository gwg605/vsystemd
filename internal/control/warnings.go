package control

import (
	"fmt"

	"github.com/gwg605/vsystemd/internal/env"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/unit"
	"github.com/gwg605/vsystemd/internal/unitpath"
)

// unitPathWarnings checks the custom output and environment file paths of a
// service unit and returns warnings for invalid, missing, or inaccessible
// paths. It never fails: problems are reported to the user as warnings.
func unitPathWarnings(store storage.Storage, parsed *unit.UnitFile, roots unitpath.Roots) []string {
	var warnings []string

	resolved, err := unitpath.ResolveService(parsed.Service, unitpath.NewValues(roots, parsed.Name))
	if err != nil {
		warnings = append(warnings, err.Error())
		return warnings
	}

	stdout, stderr, err := unit.ResolveOutputs(resolved.StandardOutput, resolved.StandardError)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else {
		for _, target := range []unit.OutputTarget{stdout, stderr} {
			if target.Kind != unit.OutputFile && target.Kind != unit.OutputAppend {
				continue
			}
			if err := store.ValidatePath(target.Path); err != nil {
				warnings = append(warnings, err.Error())
				continue
			}
			if err := store.OutputWritable(target.Path); err != nil {
				warnings = append(warnings, fmt.Sprintf("output %s: %v", target.Path, err))
			}
		}
	}

	warnings = append(warnings, env.ValidateFiles(parsed.Service.EnvironmentFiles)...)
	return warnings
}
