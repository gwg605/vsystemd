package vsystemd

import (
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	if Version != "2" {
		t.Errorf("Version = %q", Version)
	}
	if !strings.Contains(FeatureFlags, "+WIN32") {
		t.Errorf("FeatureFlags = %q", FeatureFlags)
	}
}
