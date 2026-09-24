package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type DAVAccess struct {
	Calendars bool `json:"caldav"`
	Contacts  bool `json:"carddav"`
}
type DAVAccount struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
type DAVCredential struct {
	ID       string `json:"id"`
	Password string `json:"password"`
}

func (c *Client) DAVAccess(ctx context.Context) (DAVAccess, error) {
	var out DAVAccess
	err := c.readResource(ctx, "/me/access", &out)
	return out, err
}
func (c *Client) DAVAccount(ctx context.Context) (DAVAccount, error) {
	var out DAVAccount
	err := c.readResource(ctx, "/me", &out)
	return out, err
}
func (c *Client) CreateDAVPassword(ctx context.Context, name string) (DAVCredential, error) {
	var out DAVCredential
	resp, err := c.doJSON(ctx, http.MethodPost, "/devices/webdav", map[string]string{"name": name})
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return out, statusErr(resp, "create DAV password")
	}
	err = json.NewDecoder(resp.Body).Decode(&out)
	return out, err
}
func (c *Client) RevokeDAVPassword(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/devices/"+url.PathEscape(id))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil
	}
	return okClose(resp, "revoke DAV password")
}
func (c *Client) AppleProfile(ctx context.Context, installation string, calendars, contacts bool) (string, error) {
	resp, err := c.doJSON(ctx, http.MethodPost, "/me/apple-profile", map[string]any{"server_url": c.baseURL, "installation_id": installation, "calendars": calendars, "contacts": contacts})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", statusErr(resp, "prepare Apple profile")
	}
	var out struct {
		Path string `json:"download_path"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.Path, "/apple-profile/") || strings.Contains(out.Path, "..") {
		return "", fmt.Errorf("invalid profile download path")
	}
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return "", err
	}
	u.Path = out.Path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (c *Client) AppleEnrollmentAvailable(ctx context.Context) (bool, error) {
	var out struct {
		Enabled bool `json:"enabled"`
	}
	err := c.readResource(ctx, "/me/apple-enrollment", &out)
	return out.Enabled, err
}

// Keep the credential in the HTTPS request body, never in the download URL.
func (c *Client) AppleEnrollment(ctx context.Context, installation string, calendars, contacts bool, credential DAVCredential) (string, error) {
	u, err := url.Parse(c.baseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return "", fmt.Errorf("encrypted setup requires HTTPS")
	}
	resp, err := c.doJSON(ctx, http.MethodPost, "/me/apple-enrollment", map[string]any{
		"server_url": c.baseURL, "installation_id": installation, "calendars": calendars, "contacts": contacts,
		"device_id": credential.ID, "password": credential.Password,
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		return "", statusErr(resp, "prepare Apple enrollment")
	}
	var out struct {
		Path string `json:"download_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	parts := strings.Split(out.Path, "/")
	if len(parts) != 4 || parts[0] != "" || parts[1] != "apple-enrollment" || len(parts[2]) != 64 || parts[3] != "DiscoDrive.mobileconfig" {
		return "", fmt.Errorf("invalid enrollment download path")
	}
	for _, ch := range parts[2] {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			return "", fmt.Errorf("invalid enrollment ticket")
		}
	}
	u.Path, u.RawPath, u.RawQuery, u.Fragment = out.Path, "", "", ""
	return u.String(), nil
}
