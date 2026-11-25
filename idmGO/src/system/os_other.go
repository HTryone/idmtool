package system

// osExecutable / osArgs / workingDir：按 GOOS 拆分，console 子系统无需 attach。

import (
	"os"
	"path/filepath"
)

func osExecutable() (string, error) { return os.Executable() }
func osArgs() []string              { return os.Args[1:] }
func workingDir() string {
	d, err := filepath.Abs(".")
	if err != nil {
		return ""
	}
	return d
}
