package main

import "testing"

func TestPasswordRequired(t *testing.T) {
	tests := map[string]bool{
		`NT SERVICE\app`:              false,
		`nt service\app`:              false,
		`LocalSystem`:                 false,
		`SYSTEM`:                      false,
		`NT AUTHORITY\SYSTEM`:         false,
		`NT AUTHORITY\LocalService`:   false,
		`NT AUTHORITY\NetworkService`: false,
		`LOCAL SERVICE`:               false,
		`NETWORK SERVICE`:             false,
		`CONTOSO\alice`:               true,
		`.\alice`:                     true,
	}
	for account, want := range tests {
		if got := passwordRequired(account); got != want {
			t.Errorf("passwordRequired(%q) = %v, want %v", account, got, want)
		}
	}
}

func TestResolvePasswordProvided(t *testing.T) {
	password, err := resolvePassword(`CONTOSO\alice`, "secret")
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "secret" {
		t.Errorf("password = %q", password)
	}

	password, err = resolvePassword("LocalSystem", "")
	if err != nil {
		t.Fatalf("resolvePassword() error = %v", err)
	}
	if password != "" {
		t.Errorf("password = %q", password)
	}
}
