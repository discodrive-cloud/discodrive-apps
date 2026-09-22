//go:build darwin || linux

package fullsync

import (
	"fmt"
	"os"
	"syscall"
)

func identity(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	st := info.Sys().(*syscall.Stat_t)
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino), nil
}
