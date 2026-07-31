// Package protocol — HTTP client for the discodrive server (implements engine.Source).
package protocol

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"

	"discodrive.org/daemon/internal/engine"
)

// defaultHTTPClient returns the HTTP client used to talk to the server. By default it
// uses strict system TLS validation. Set DISCODRIVE_INSECURE_TLS=1 to accept ANY
// certificate — intended for local testing against a self-signed / LAN-IP server only.
func defaultHTTPClient() *http.Client {
	tlsConf := &tls.Config{
		// Restrict key-exchange to classical curves, excluding the post-quantum
		// X25519MLKEM768 hybrid that Go offers by default since 1.24. Some TLS
		// terminators / middleboxes choke on the larger ClientHello and abort the
		// handshake (remote error: tls: handshake failure); leaving it out keeps
		// pairing/sync working against those servers.
		CurvePreferences: []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384, tls.CurveP521},
	}
	if v := os.Getenv("DISCODRIVE_INSECURE_TLS"); v == "1" || v == "true" {
		tlsConf.InsecureSkipVerify = true //nolint:gosec // opt-in via env for local testing
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConf}}
}

// scopeHeader opts this client into the user's configured sync scope. The server applies
// the scope ONLY to callers that send it — so the daemon (and the future mobile button-app,
// which runs this same core) sync the chosen folder, while browser/web/WebDAV clients omit
// the header and keep seeing the whole vault. Always sent: when no scope is configured the
// server resolves it to the whole vault anyway.
const scopeHeader = "X-Discodrive-Scope"

// Client talks to the server using a device token and caches the session JWT.
type Client struct {
	baseURL     string
	deviceToken string
	hc          *http.Client
	sendScope   bool

	mu  sync.Mutex
	jwt string
}

func New(baseURL, deviceToken string) *Client {
	return &Client{baseURL: baseURL, deviceToken: deviceToken, hc: defaultHTTPClient(), sendScope: true}
}

// NewUnscoped is like New but never sends X-Discodrive-Scope, so the server returns the whole
// vault (used by the file browser, which navigates everything rather than one synced folder).
func NewUnscoped(baseURL, deviceToken string) *Client {
	return &Client{baseURL: baseURL, deviceToken: deviceToken, hc: defaultHTTPClient(), sendScope: false}
}

func (c *Client) token(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jwt != "" {
		return c.jwt, nil
	}
	body, _ := json.Marshal(map[string]string{"device_token": c.deviceToken})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth/device/token", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("device token exchange: %s", resp.Status)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	c.jwt = out.Token
	return c.jwt, nil
}

func (c *Client) do(ctx context.Context, method, path string) (*http.Response, error) {
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		if c.sendScope {
			req.Header.Set(scopeHeader, "1")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.mu.Lock()
			c.jwt = ""
			c.mu.Unlock()
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("authorization failed")
}

type changeJSON struct {
	Seq         int64  `json:"seq"`
	Op          string `json:"op"`
	NodeID      string `json:"node_id"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	Version     int64  `json:"version"`
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
	Deleted     bool   `json:"deleted"`
}

func (c *Client) Changes(ctx context.Context, since int64, limit int) ([]engine.Change, int64, bool, error) {
	q := url.Values{}
	q.Set("since", strconv.FormatInt(since, 10))
	q.Set("limit", strconv.Itoa(limit))
	resp, err := c.do(ctx, http.MethodGet, "/sync/changes?"+q.Encode())
	if err != nil {
		return nil, 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, false, fmt.Errorf("/sync/changes: %s", resp.Status)
	}
	var body struct {
		Changes []changeJSON `json:"changes"`
		Cursor  int64        `json:"cursor"`
		HasMore bool         `json:"has_more"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, false, err
	}
	out := make([]engine.Change, 0, len(body.Changes))
	for _, ch := range body.Changes {
		out = append(out, engine.Change{
			Seq: ch.Seq, Op: ch.Op, NodeID: ch.NodeID, RelPath: ch.Path,
			IsDir: ch.IsDir, Version: ch.Version, ContentHash: ch.ContentHash, Size: ch.Size, Deleted: ch.Deleted,
		})
	}
	return out, body.Cursor, body.HasMore, nil
}

func (c *Client) Download(ctx context.Context, nodeID string, w io.Writer) error {
	resp, err := c.do(ctx, http.MethodGet, "/files/"+url.PathEscape(nodeID)+"/content")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("/files/%s/content: %s", nodeID, resp.Status)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

var _ engine.Source = (*Client)(nil)
var _ engine.Sink = (*Client)(nil)
var _ engine.MoveSink = (*Client)(nil)

func (c *Client) clearJWT() {
	c.mu.Lock()
	c.jwt = ""
	c.mu.Unlock()
}

// replayableBody prepares a request body that can be sent more than once — PushFile
// retries after a 401, and by then the first attempt has already consumed the reader.
//
// A seekable source (engine.PushLocal hands us an *os.File) is streamed straight from
// disk and rewound between attempts: syncing a multi-gigabyte file must not pull it
// through RAM, which matters most on the phone builds. Only a source that cannot be
// rewound is buffered, because there is no other way to send it twice.
//
// Returns a factory for fresh readers and the body length, or -1 if it is unknown.
func replayableBody(r io.Reader) (func() (io.Reader, error), int64, error) {
	if s, ok := r.(io.Seeker); ok {
		if start, err := s.Seek(0, io.SeekCurrent); err == nil {
			if end, err := s.Seek(0, io.SeekEnd); err == nil {
				if _, err := s.Seek(start, io.SeekStart); err == nil {
					first := true
					return func() (io.Reader, error) {
						if first {
							first = false // already positioned; avoid a redundant syscall
						} else if _, err := s.Seek(start, io.SeekStart); err != nil {
							return nil, err
						}
						// NopCloser matters: net/http closes a request body that implements
						// io.Closer, which would shut the caller's *os.File after the first
						// attempt — breaking the rewind and the caller's own deferred Close.
						return io.NopCloser(r), nil
					}, end - start, nil
				}
			}
		}
		// Seeking failed part-way: the offset may be anywhere now, so fall through to
		// buffering whatever is still readable rather than sending a truncated body.
	}
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	return func() (io.Reader, error) { return bytes.NewReader(buf), nil }, int64(len(buf)), nil
}

func (c *Client) PushFile(ctx context.Context, relPath string, baseVersion *int64, r io.Reader) (engine.RemoteNode, bool, error) {
	newBody, size, err := replayableBody(r)
	if err != nil {
		return engine.RemoteNode{}, false, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return engine.RemoteNode{}, false, err
		}
		body, err := newBody()
		if err != nil {
			return engine.RemoteNode{}, false, err
		}
		u := c.baseURL + "/sync/file?path=" + url.QueryEscape(relPath)
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, u, body)
		if err != nil {
			return engine.RemoteNode{}, false, err
		}
		// Declaring the length keeps the request out of chunked encoding and makes
		// net/http fail loudly if the body turns out shorter than promised, instead of
		// letting a short file reach the server looking complete.
		req.ContentLength = size
		req.Header.Set("Authorization", "Bearer "+tok)
		if c.sendScope {
			req.Header.Set(scopeHeader, "1")
		}
		if baseVersion != nil {
			req.Header.Set("X-Base-Version", strconv.FormatInt(*baseVersion, 10))
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return engine.RemoteNode{}, false, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.clearJWT()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return engine.RemoteNode{}, false, fmt.Errorf("PUT /sync/file: %s", resp.Status)
		}
		var out struct {
			Node struct {
				ID      string `json:"id"`
				Version int64  `json:"version"`
			} `json:"node"`
			Conflicted bool `json:"conflicted"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return engine.RemoteNode{}, false, err
		}
		return engine.RemoteNode{NodeID: out.Node.ID, Version: out.Node.Version}, out.Conflicted, nil
	}
	return engine.RemoteNode{}, false, fmt.Errorf("authorization failed")
}

func (c *Client) EnsureDir(ctx context.Context, relPath string) (engine.RemoteNode, error) {
	body, _ := json.Marshal(map[string]string{"path": relPath})
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return engine.RemoteNode{}, err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/sync/dir", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		if c.sendScope {
			req.Header.Set(scopeHeader, "1")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return engine.RemoteNode{}, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.clearJWT()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			return engine.RemoteNode{}, fmt.Errorf("POST /sync/dir: %s", resp.Status)
		}
		var out struct {
			Node struct {
				ID      string `json:"id"`
				Version int64  `json:"version"`
			} `json:"node"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return engine.RemoteNode{}, err
		}
		return engine.RemoteNode{NodeID: out.Node.ID, Version: out.Node.Version}, nil
	}
	return engine.RemoteNode{}, fmt.Errorf("authorization failed")
}

func (c *Client) DeleteRemote(ctx context.Context, relPath string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/sync/file?path="+url.QueryEscape(relPath))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("DELETE /sync/file: %s", resp.Status)
	}
	return nil
}

// Events opens an SSE stream at /sync/events (for the daemon listener). The caller reads the body.
func (c *Client) Events(ctx context.Context) (*http.Response, error) {
	return c.do(ctx, http.MethodGet, "/sync/events")
}

// Language fetches the user's preferred language from GET /me/language.
// Returns "en" on any error (server unreachable, not paired, etc.).
func (c *Client) Language(ctx context.Context) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, "/me/language")
	if err != nil {
		return "en", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "en", fmt.Errorf("/me/language: %s", resp.Status)
	}
	var out struct {
		Language string `json:"language"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "en", err
	}
	if out.Language == "" {
		return "en", nil
	}
	return out.Language, nil
}

// SyncMeta returns the server's current scope epoch. The epoch bumps whenever the user
// changes their sync scope (folder or on/off); the daemon compares it against the stored
// epoch to decide when to reconcile the local mirror.
func (c *Client) SyncMeta(ctx context.Context) (int64, error) {
	resp, err := c.do(ctx, http.MethodGet, "/sync/meta")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("/sync/meta: %s", resp.Status)
	}
	var out struct {
		ScopeEpoch int64 `json:"scope_epoch"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.ScopeEpoch, nil
}

// --- REST file operations (used by the mobile Browser facade) ---

// doJSON sends a JSON body with auth, retrying once on 401. Caller closes resp.Body.
func (c *Client) doJSON(ctx context.Context, method, path string, body any) (*http.Response, error) {
	raw, _ := json.Marshal(body)
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return nil, err
		}
		req, _ := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		if c.sendScope {
			req.Header.Set(scopeHeader, "1")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.clearJWT()
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("authorization failed")
}

func okClose(resp *http.Response, what string) error {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s", what, resp.Status)
	}
	return nil
}

// --- chunked upload (/upload/*), mirrors the web UI's resumable protocol ---

// UploadInit starts a chunked upload of name under parentID ("" = root). Returns the
// server-assigned upload id and the next chunk index to send (0 for a fresh upload).
func (c *Client) UploadInit(ctx context.Context, parentID, name string) (string, int, error) {
	body := map[string]any{"name": name}
	if parentID != "" {
		body["parent_id"] = parentID
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/upload/init", body)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("/upload/init: %s", resp.Status)
	}
	var out struct {
		UploadID  string `json:"upload_id"`
		NextChunk int    `json:"next_chunk"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, err
	}
	return out.UploadID, out.NextChunk, nil
}

// countingReader reports cumulative bytes read via onRead, used to surface upload
// progress as the request body is streamed to the socket.
type countingReader struct {
	r      io.Reader
	n      int64
	onRead func(sent int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	k, err := c.r.Read(p)
	if k > 0 {
		c.n += int64(k)
		if c.onRead != nil {
			c.onRead(c.n)
		}
	}
	return k, err
}

// UploadChunk sends chunk n (the bytes from r) and returns the next expected chunk
// index. The chunk is buffered so the request can be retried once on a 401. onSent,
// if non-nil, is called with cumulative bytes sent within this chunk (for progress).
func (c *Client) UploadChunk(ctx context.Context, uploadID string, n int, r io.Reader, onSent func(sent int64)) (int, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return 0, err
	}
	path := "/upload/" + url.PathEscape(uploadID) + "/chunk/" + strconv.Itoa(n)
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return 0, err
		}
		var body io.Reader = bytes.NewReader(buf)
		if onSent != nil {
			body = &countingReader{r: body, onRead: onSent}
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, body)
		req.ContentLength = int64(len(buf))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/octet-stream")
		if c.sendScope {
			req.Header.Set(scopeHeader, "1")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return 0, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.clearJWT()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return 0, fmt.Errorf("/upload chunk %d: %s", n, resp.Status)
		}
		var out struct {
			NextChunk int `json:"next_chunk"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return 0, err
		}
		return out.NextChunk, nil
	}
	return 0, fmt.Errorf("authorization failed")
}

// UploadStatus returns the next chunk index the server expects (for resume).
func (c *Client) UploadStatus(ctx context.Context, uploadID string) (int, error) {
	resp, err := c.do(ctx, http.MethodGet, "/upload/"+url.PathEscape(uploadID))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("/upload status: %s", resp.Status)
	}
	var out struct {
		NextChunk int `json:"next_chunk"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.NextChunk, nil
}

// UploadComplete finalizes the upload, creating the server file.
func (c *Client) UploadComplete(ctx context.Context, uploadID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/upload/"+url.PathEscape(uploadID)+"/complete")
	if err != nil {
		return err
	}
	return okClose(resp, "/upload complete")
}

// UploadAbort discards an in-progress upload session.
func (c *Client) UploadAbort(ctx context.Context, uploadID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/upload/"+url.PathEscape(uploadID))
	if err != nil {
		return err
	}
	return okClose(resp, "/upload abort")
}

// CreateFolder creates a folder under parentID ("" = root).
func (c *Client) CreateFolder(ctx context.Context, parentID, name string) error {
	body := map[string]any{"name": name}
	if parentID != "" {
		body["parent_id"] = parentID
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/files/folder", body)
	if err != nil {
		return err
	}
	return okClose(resp, "POST /files/folder")
}

// RenameNode renames a node.
func (c *Client) RenameNode(ctx context.Context, nodeID, newName string) error {
	resp, err := c.doJSON(ctx, http.MethodPatch, "/files/"+url.PathEscape(nodeID)+"/rename", map[string]string{"name": newName})
	if err != nil {
		return err
	}
	return okClose(resp, "PATCH rename")
}

// MoveNode moves a node under newParentID ("" = root).
func (c *Client) MoveNode(ctx context.Context, nodeID, newParentID string) error {
	var pid *string
	if newParentID != "" {
		pid = &newParentID
	}
	resp, err := c.doJSON(ctx, http.MethodPatch, "/files/"+url.PathEscape(nodeID)+"/move", map[string]any{"parent_id": pid})
	if err != nil {
		return err
	}
	return okClose(resp, "PATCH move")
}

// DeleteNode soft-deletes a node by id.
func (c *Client) DeleteNode(ctx context.Context, nodeID string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/files/"+url.PathEscape(nodeID))
	if err != nil {
		return err
	}
	return okClose(resp, "DELETE /files")
}

// UploadFile uploads a file into parentID ("" = root) via multipart.
func (c *Client) UploadFile(ctx context.Context, parentID, name string, r io.Reader) error {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if parentID != "" {
		_ = mw.WriteField("parent_id", parentID)
	}
	_ = mw.WriteField("name", name)
	fw, _ := mw.CreateFormFile("file", name)
	if _, err := io.Copy(fw, r); err != nil {
		return err
	}
	mw.Close()
	for attempt := 0; attempt < 2; attempt++ {
		tok, err := c.token(ctx)
		if err != nil {
			return err
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/files/upload", bytes.NewReader(buf.Bytes()))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		if c.sendScope {
			req.Header.Set(scopeHeader, "1")
		}
		resp, err := c.hc.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			c.clearJWT()
			continue
		}
		return okClose(resp, "POST /files/upload")
	}
	return fmt.Errorf("authorization failed")
}
