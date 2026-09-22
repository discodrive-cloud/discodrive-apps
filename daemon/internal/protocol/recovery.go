package protocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

type TrashItem struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	IsDir     bool    `json:"is_dir"`
	Size      *int64  `json:"size"`
	DeletedAt *string `json:"deleted_at"`
}
type FileVersion struct {
	Version         int64  `json:"version"`
	Size            *int64 `json:"size"`
	IsConflictLoser bool   `json:"is_conflict_loser"`
}
type Share struct {
	ID        string  `json:"share_id"`
	Kind      string  `json:"kind"`
	Email     string  `json:"email,omitempty"`
	ExpiresAt *string `json:"expires_at,omitempty"`
}
type ShareResult struct {
	ID    string `json:"share_id"`
	Token string `json:"token,omitempty"`
	URL   string `json:"url,omitempty"`
}

func (c *Client) readResource(ctx context.Context, path string, into any) error {
	resp, err := c.do(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusErr(resp, path)
	}
	return json.NewDecoder(resp.Body).Decode(into)
}
func (c *Client) mutateResource(ctx context.Context, method, path string, body any) error {
	resp, err := c.doJSON(ctx, method, path, body)
	if err != nil {
		return err
	}
	return okClose(resp, path)
}
func (c *Client) Trash(ctx context.Context) ([]TrashItem, error) {
	out := []TrashItem{}
	err := c.readResource(ctx, "/files/trash", &out)
	return out, err
}
func (c *Client) Undelete(ctx context.Context, id string) error {
	return c.mutateResource(ctx, http.MethodPost, "/files/"+url.PathEscape(id)+"/undelete", nil)
}
func (c *Client) Purge(ctx context.Context, id string) error {
	return c.mutateResource(ctx, http.MethodDelete, "/files/"+url.PathEscape(id)+"/purge", nil)
}
func (c *Client) EmptyTrash(ctx context.Context) error {
	return c.mutateResource(ctx, http.MethodDelete, "/files/trash", nil)
}
func (c *Client) Versions(ctx context.Context, id string) ([]FileVersion, error) {
	out := []FileVersion{}
	err := c.readResource(ctx, "/files/"+url.PathEscape(id)+"/versions", &out)
	return out, err
}
func (c *Client) RestoreVersion(ctx context.Context, id string, version int64) error {
	return c.mutateResource(ctx, http.MethodPost, "/files/"+url.PathEscape(id)+"/restore", map[string]int64{"version": version})
}
func (c *Client) Shares(ctx context.Context, id string) ([]Share, error) {
	out := []Share{}
	err := c.readResource(ctx, "/files/"+url.PathEscape(id)+"/shares", &out)
	return out, err
}
func (c *Client) CreateShare(ctx context.Context, id, email string, days int) (ShareResult, error) {
	body := map[string]any{"access": "read"}
	if email == "" {
		body["link"] = true
	} else {
		body["email"] = email
	}
	if days > 0 {
		body["expires_in_seconds"] = days * 86400
	}
	path := "/files/" + url.PathEscape(id) + "/share"
	resp, err := c.doJSON(ctx, http.MethodPost, path, body)
	if err != nil {
		return ShareResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return ShareResult{}, statusErr(resp, path)
	}
	var out ShareResult
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, err
	}
	if out.Token != "" {
		out.URL = c.baseURL + "/s/" + url.PathEscape(out.Token)
	}
	return out, nil
}
func (c *Client) RevokeShare(ctx context.Context, id string) error {
	return c.mutateResource(ctx, http.MethodDelete, "/shares/"+url.PathEscape(id), nil)
}
