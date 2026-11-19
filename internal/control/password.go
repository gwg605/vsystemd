package control

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func resolvePassword(provided, passwordFile string, stdin bool) (string, error) {
	sources := 0
	if provided != "" {
		sources++
	}
	if passwordFile != "" {
		sources++
	}
	if stdin {
		sources++
	}
	if sources > 1 {
		return "", errors.New("specify only one of --password, --password-file, --password-stdin")
	}

	switch {
	case provided != "":
		return provided, nil
	case passwordFile != "":
		return readPasswordFile(passwordFile)
	case stdin:
		return readPassword(os.Stdin)
	default:
		return "", nil
	}
}

func readPasswordFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	return trimPassword(string(data)), nil
}

func readPassword(r io.Reader) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return trimPassword(string(data)), nil
}

func trimPassword(value string) string {
	return strings.TrimRight(value, "\r\n")
}
