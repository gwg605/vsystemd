package runner

import (
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProcessHelper(t *testing.T) {
	args, ok := helperArgs(os.Args)
	if !ok {
		return
	}

	switch args[0] {
	case "record":
		code := 0
		if len(args) > 3 {
			code, _ = strconv.Atoi(args[3])
		}
		file, err := os.OpenFile(args[1], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			os.Exit(2)
		}
		fmt.Fprintln(file, args[2])
		file.Close()
		os.Exit(code)
	case "recordenv":
		if err := os.WriteFile(args[1], []byte(os.Getenv(args[2])), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "recordcwd":
		wd, err := os.Getwd()
		if err != nil {
			os.Exit(2)
		}
		if err := os.WriteFile(args[1], []byte(wd), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	case "output":
		fmt.Fprint(os.Stdout, args[1])
		fmt.Fprint(os.Stderr, args[2])
		os.Exit(0)
	case "failonce":
		if _, err := os.Stat(args[1]); err == nil {
			os.Exit(0)
		}
		if err := os.WriteFile(args[1], []byte("failed"), 0o644); err != nil {
			os.Exit(2)
		}
		os.Exit(1)
	case "sleep":
		duration, err := time.ParseDuration(args[1])
		if err != nil {
			os.Exit(2)
		}
		time.Sleep(duration)
		os.Exit(0)
	}
	os.Exit(2)
}

func helperArgs(argv []string) ([]string, bool) {
	index := 0
	for index < len(argv) && argv[index] != "--" {
		index++
	}
	if index+1 >= len(argv) {
		return nil, false
	}
	return argv[index+1:], true
}

func helperCommand(args ...string) string {
	parts := []string{quoteArg(os.Args[0]), "-test.run=TestProcessHelper", "--"}
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteArg(arg string) string {
	if arg == "" || strings.ContainsAny(arg, " \t") {
		return `"` + arg + `"`
	}
	return arg
}

func TestWriteStopConfirmation(t *testing.T) {
	var buffer strings.Builder
	if err := writeStopConfirmation(&buffer); err != nil {
		t.Fatalf("writeStopConfirmation() error = %v", err)
	}
	if buffer.String() != "Y\r\n" {
		t.Errorf("stdin = %q", buffer.String())
	}
}

func TestParseCommand(t *testing.T) {
	cmd, err := parseCommand(`-"C:\Program Files\app.exe" --flag "hello world"`)
	if err != nil {
		t.Fatalf("parseCommand() error = %v", err)
	}
	if !cmd.ignoreFailure {
		t.Error("ignoreFailure = false")
	}
	if cmd.path != `C:\Program Files\app.exe` {
		t.Errorf("path = %q", cmd.path)
	}
	if !reflect.DeepEqual(cmd.args, []string{"--flag", "hello world"}) {
		t.Errorf("args = %v", cmd.args)
	}
}
