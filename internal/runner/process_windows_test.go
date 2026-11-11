//go:build windows

package runner

import "testing"

func TestEnsureConsole(t *testing.T) {
	ensureConsole()
	if !hasConsole() {
		t.Fatal("console is not available")
	}
}
