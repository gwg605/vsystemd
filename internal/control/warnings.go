package control

import (
	"fmt"
	"os"
	"strings"

	"github.com/gwg605/vsystemd/internal/logwriter"
	"github.com/gwg605/vsystemd/internal/unit"
)

// unitPathWarnings checks the custom output and environment file paths of a
// service unit and returns warnings for invalid, missing, or inaccessible
// paths. It never fails: problems are reported to the user as warnings.
func unitPathWarnings(parsed *unit.UnitFile) []string {
	var warnings []string

	stdout, stderr, err := logwriter.ResolveServiceOutputs(parsed.Service)
	if err != nil {
		warnings = append(warnings, err.Error())
	} else {
		for _, target := range []logwriter.OutputTarget{stdout, stderr} {
			if target.Kind != logwriter.OutputFile && target.Kind != logwriter.OutputAppend {
				continue
			}
			if err := logwriter.CheckOutputWritable(target.Path); err != nil {
				warnings = append(warnings, fmt.Sprintf("output %s: %v", target.Path, err))
			}
		}
	}

	for _, file := range parsed.Service.EnvironmentFiles {
		optional := strings.HasPrefix(file, "-")
		path := strings.TrimPrefix(file, "-")
		if err := logwriter.ValidateUnitPath(path); err != nil {
			warnings = append(warnings, err.Error())
			continue
		}
		if err := logwriter.CheckEnvReadable(path); err != nil {
			if optional && os.IsNotExist(err) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("environment file %s: %v", path, err))
		}
	}
	return warnings
}
