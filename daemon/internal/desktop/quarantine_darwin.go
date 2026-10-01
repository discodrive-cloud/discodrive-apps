package desktop

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// quarantineXattr is the attribute Gatekeeper consults before opening a file.
const quarantineXattr = "com.apple.quarantine"

// writeQuarantine sets com.apple.quarantine the way a browser download does.
func writeQuarantine(path string) error {
	value := fmt.Sprintf("0083;%x;DiscoDrive;", time.Now().Unix())
	return unix.Setxattr(path, quarantineXattr, []byte(value), 0)
}
