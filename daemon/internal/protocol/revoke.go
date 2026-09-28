package protocol

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// RevokeDevice removes this device from the account on the server, so its device token
// stops working. Called on sign-out: deleting the token only locally left it valid for
// anyone holding a copy. A device the server no longer accepts counts as revoked.
func (c *Client) RevokeDevice(ctx context.Context) error {
	tok, err := c.token(ctx)
	var se *StatusError
	if errors.As(err, &se) && (se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden) {
		return nil
	}
	if err != nil {
		return err
	}
	id, err := deviceIDFromJWT(tok)
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, http.MethodDelete, "/devices/"+url.PathEscape(id))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent, http.StatusNotFound, http.StatusUnauthorized:
		return nil
	}
	return statusErr(resp, "revoke device")
}

// deviceIDFromJWT reads the device id claim ("did") the server puts in a device session. Only
// our own session token is read here; its signature is the server's business.
func deviceIDFromJWT(tok string) (string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("revoke device: malformed session token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return "", fmt.Errorf("revoke device: %w", err)
	}
	var claims struct {
		DeviceID string `json:"did"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("revoke device: %w", err)
	}
	if claims.DeviceID == "" {
		return "", fmt.Errorf("revoke device: session carries no device id")
	}
	return claims.DeviceID, nil
}
