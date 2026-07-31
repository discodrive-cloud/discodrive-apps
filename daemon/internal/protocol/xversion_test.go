package protocol

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Cross-version check: drive this client against a REAL server binary of another version,
// to catch protocol drift that unit tests with a fake server cannot. Skipped unless both
// env vars are set, so the normal suite stays hermetic.
//
// Setting up the other side (roughly ten minutes):
//
//	# 1. a throwaway Postgres
//	docker run -d --name ddx-pg -e POSTGRES_USER=kf -e POSTGRES_PASSWORD=kf \
//	    -e POSTGRES_DB=kf -p 55433:5432 postgres:16-alpine
//
//	# 2. the server version you want to test against, from a worktree of the server repo
//	git worktree add /tmp/oldserver <commit> && (cd /tmp/oldserver && go build -o /tmp/server-old ./cmd/server)
//	DATABASE_URL='postgres://kf:kf@localhost:55433/kf?sslmode=disable' \
//	  JWT_SECRET=0123456789abcdef0123456789abcdef0123456789 \
//	  SETTINGS_ENCRYPTION_KEY=0123456789abcdef0123456789abcdef BASE_DOMAIN=localhost \
//	  STORAGE_ROOT=/tmp/ddx-data APP_PORT=18080 XACCEL_ENABLED=false /tmp/server-old
//
//	# 3. an admin and a paired device (device_token comes back from the last call)
//	curl -sX POST localhost:18080/setup/admin -H 'Content-Type: application/json' \
//	     -d '{"email":"t@x.test","password":"password12345"}'
//	JWT=$(curl -sX POST localhost:18080/auth/login -H 'Content-Type: application/json' \
//	     -d '{"email":"t@x.test","password":"password12345"}' | jq -r .token)
//	P=$(curl -sX POST localhost:18080/pair/init -H 'Content-Type: application/json' \
//	     -d '{"name":"xtest","kind":"desktop"}')
//	curl -sX POST "localhost:18080/pair/$(jq -r .user_code <<<"$P")/approve" -H "Authorization: Bearer $JWT"
//	curl -sX POST localhost:18080/pair/token -H 'Content-Type: application/json' \
//	     -d "{\"device_code\":\"$(jq -r .device_code <<<"$P")\"}"
//
//	# 4. run
//	DDX_SERVER_URL=http://localhost:18080 DDX_DEVICE_TOKEN=kfd_... \
//	    go test ./internal/protocol/ -run XVersion -v
//
// Safety: nothing here reads the daemon's config file — the URL and token come from the
// environment and every local path is a t.TempDir(), so a real sync folder cannot be
// touched. Everything uploaded goes into one throwaway folder on the server which is
// deleted afterwards, so pointing this at a populated server is survivable. It is still
// meant for a scratch server, not production.
const xversionDir = "_xversion-test"

func xversionClient(t *testing.T) (*Client, string) {
	t.Helper()
	url, tok := os.Getenv("DDX_SERVER_URL"), os.Getenv("DDX_DEVICE_TOKEN")
	if url == "" || tok == "" {
		t.Skip("set DDX_SERVER_URL and DDX_DEVICE_TOKEN to run the cross-version check")
	}
	c := New(url, tok)

	// One folder for the whole test, removed on the way out so repeated runs do not pile
	// up and a mis-pointed run leaves nothing behind.
	dir, err := c.EnsureDir(context.Background(), xversionDir)
	if err != nil {
		t.Fatalf("EnsureDir %s: %v", xversionDir, err)
	}
	t.Cleanup(func() {
		if err := c.DeleteNode(context.Background(), dir.NodeID); err != nil {
			t.Logf("cleanup: could not delete %s (%s): %v", xversionDir, dir.NodeID, err)
		}
	})
	return c, dir.NodeID
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func xversionFixture(t *testing.T, name string, size int) (*os.File, []byte) {
	t.Helper()
	payload := bytes.Repeat([]byte("kf"), size/2)
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f, payload
}

// TestXVersionSyncPush covers the daemon's main route: PUT /sync/file carrying
// X-Modified-At. An older server must ignore the header and still store the file.
func TestXVersionSyncPush(t *testing.T) {
	c, _ := xversionClient(t)
	ctx := context.Background()
	f, payload := xversionFixture(t, "sync.bin", 1_000_000)

	rn, conflicted, err := c.PushFile(ctx, xversionDir+"/sync.bin", nil, f,
		time.Date(2019, 6, 15, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PushFile: %v", err)
	}
	if conflicted {
		t.Fatal("unexpected conflict on a fresh path")
	}

	var back bytes.Buffer
	if err := c.Download(ctx, rn.NodeID, &back); err != nil {
		t.Fatalf("download: %v", err)
	}
	if sha(back.Bytes()) != sha(payload) {
		t.Fatalf("round-trip mismatch: got %d bytes, sent %d", back.Len(), len(payload))
	}
	t.Logf("sync push OK: %d bytes, node %s", len(payload), rn.NodeID)
}

// TestXVersionMultipartUpload covers POST /files/upload with the extra modified_at field
// and an exact Content-Length. An older server must ignore the field and accept the body.
func TestXVersionMultipartUpload(t *testing.T) {
	c, parent := xversionClient(t)
	f, payload := xversionFixture(t, "mp.bin", 900_000)

	err := c.UploadFile(context.Background(), parent, "multipart.bin", f,
		time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		t.Fatalf("UploadFile: %v", err)
	}
	t.Logf("multipart upload OK: %d bytes", len(payload))
}

// TestXVersionChunkedUpload covers init → chunk → complete with the declared size and
// date. An older server ignores both extra fields; a newer one verifies the size.
func TestXVersionChunkedUpload(t *testing.T) {
	c, parent := xversionClient(t)
	ctx := context.Background()
	payload := bytes.Repeat([]byte("ch"), 500_000)

	id, next, err := c.UploadInit(ctx, parent, "chunked.bin", int64(len(payload)),
		time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC))
	if err != nil {
		t.Fatalf("UploadInit: %v", err)
	}
	if next != 0 {
		t.Fatalf("fresh session starts at chunk %d, want 0", next)
	}
	if _, err := c.UploadChunk(ctx, id, 0, bytes.NewReader(payload), nil); err != nil {
		t.Fatalf("UploadChunk: %v", err)
	}
	if err := c.UploadComplete(ctx, id); err != nil {
		t.Fatalf("UploadComplete: %v", err)
	}
	t.Logf("chunked upload OK: %d bytes, session %s", len(payload), id)
}
