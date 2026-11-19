package unit

import (
	"reflect"
	"testing"
)

func TestDependencies(t *testing.T) {
	file := &UnitFile{
		Requires: []string{"db.service", "cache.service"},
		After:    []string{"network.target", "db.service", "web.service"},
	}
	want := []string{"db", "cache", "web"}
	if got := file.Dependencies(); !reflect.DeepEqual(got, want) {
		t.Errorf("Dependencies() = %v, want %v", got, want)
	}
}
