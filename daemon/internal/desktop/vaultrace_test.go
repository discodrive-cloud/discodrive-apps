package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"

	"discodrive.org/daemon/internal/vaultmgr"
)

// Every bound call runs in its own goroutine: re-opening a vault (which inspects the
// session) while it is being closed must not race on the session's fields.
func TestVaultReopenDuringCloseIsRaceFree(t *testing.T) {
	c, _, _, _ := openCloseFixture(t)
	ctx := context.Background()
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_, _ = c.openVaultCore(ctx, "vault", func(*vaultmgr.Manager, vaultmgr.VaultInfo) (string, error) {
				return "", errors.New("no second unlock in this test")
			})
		}
	}()
	err := c.CloseVault(ctx, "vault")
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
}
