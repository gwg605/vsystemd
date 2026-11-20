package control

import (
	"fmt"

	"github.com/gwg605/vsystemd/internal/env"
	"github.com/gwg605/vsystemd/internal/storage"
	"github.com/gwg605/vsystemd/internal/unit"
)

// unitPathWarnings checks the custom output and environment file paths of a
// service unit and returns warnings for invalid, missing, or inaccessible
// paths. It never fails: problems are reported to the user as warnings.
func unitPathWarnings(store storage.Storage, parsed *unit.UnitFile) []string {
	var warnings []string

	stdout, stderr, err := parsed.Service.ResolveOutputs()
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
