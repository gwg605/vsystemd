package vsystemd

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionText string

var Version = strings.TrimSpace(versionText)

const FeatureFlags = "+WIN32 +SCM +PIPE +TIMERS +LOGROTATE"
