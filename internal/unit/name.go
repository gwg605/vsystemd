package unit

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Type uint8

const (
	TypeUnknown Type = iota
	TypeService
	TypeTimer
)

// MaxNameLength limits the unit name (the part before the extension).
const MaxNameLength = 64

func (t Type) Extension() string {
	switch t {
	case TypeService:
		return ".service"
	case TypeTimer:
		return ".timer"
	default:
		return ""
	}
}

// ID identifies a unit by its name (without the extension) and its type.
type ID struct {
	Name string
	Type Type
}

// ParseID splits a unit file name into a validated ID.
func ParseID(fileName string) (ID, error) {
	extension := filepath.Ext(fileName)
	id := ID{
		Name: strings.TrimSuffix(fileName, extension),
		Type: typeFromExtension(extension),
	}
	if err := id.Validate(); err != nil {
		return ID{}, err
	}
	return id, nil
}

func (id ID) Validate() error {
	if id.Name == "" {
		return fmt.Errorf("unit name is empty")
	}
	if len(id.Name) > MaxNameLength {
		return fmt.Errorf("unit name %q exceeds %d characters", id.Name, MaxNameLength)
	}
	for _, r := range id.Name {
		if !validNameRune(r) {
			return fmt.Errorf("unit name %q contains invalid character %q", id.Name, r)
		}
	}
	if id.Type != TypeService && id.Type != TypeTimer {
		return fmt.Errorf("unit %q has an unknown type", id.Name)
	}
	return nil
}

func (id ID) String() string {
	return id.Name + id.Type.Extension()
}

func typeFromExtension(extension string) Type {
	switch strings.ToLower(extension) {
	case ".service":
		return TypeService
	case ".timer":
		return TypeTimer
	default:
		return TypeUnknown
	}
}

func validNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '_' || r == '-':
		return true
	default:
		return false
	}
}
