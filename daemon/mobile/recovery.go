package mobile

import (
	"context"
	"encoding/json"
)

// Recovery and sharing use the same authenticated browser session as file operations.
func (b *Browser) Trash() (string, error) {
	out, err := b.client.Trash(context.Background())
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) Versions(id string) (string, error) {
	out, err := b.client.Versions(context.Background(), id)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) Undelete(id string) error { return b.client.Undelete(context.Background(), id) }
func (b *Browser) Purge(id string) error    { return b.client.Purge(context.Background(), id) }
func (b *Browser) EmptyTrash() error        { return b.client.EmptyTrash(context.Background()) }
func (b *Browser) RestoreVersion(id string, version int64) error {
	return b.client.RestoreVersion(context.Background(), id, version)
}
func (b *Browser) Shares(id string) (string, error) {
	out, err := b.client.Shares(context.Background(), id)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) CreateShare(id, email string, days int) (string, error) {
	out, err := b.client.CreateShare(context.Background(), id, email, days)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) RevokeShare(id string) error { return b.client.RevokeShare(context.Background(), id) }
