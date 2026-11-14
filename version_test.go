package vsystemd

import "testing"

func TestVersion(t *testing.T) {
	if Version != "1" {
		t.Errorf("Version = %q", Version)
	}
}
