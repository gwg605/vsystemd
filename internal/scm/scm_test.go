package scm

import "testing"

func TestNewAccount(t *testing.T) {
	if got := NewAccount(`CONTOSO\alice`, "app"); got != `CONTOSO\alice` {
		t.Errorf("NewAccount() = %q", got)
	}
	if got := NewAccount("", "app"); got != `NT SERVICE\app` {
		t.Errorf("NewAccount() = %q", got)
	}
}
