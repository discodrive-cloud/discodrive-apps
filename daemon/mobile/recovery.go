package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"discodrive.org/daemon/internal/protocol"
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
		return "", b.forgetIfGone(id, err)
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) Undelete(id string) error { return b.client.Undelete(context.Background(), id) }
func (b *Browser) Purge(id string) error {
	return trashBlocked(b.client.Purge(context.Background(), id))
}
func (b *Browser) EmptyTrash() error { return trashBlocked(b.client.EmptyTrash(context.Background())) }
func (b *Browser) RestoreVersion(id string, version int64) error {
	// A missing version, or a folder, is answered with the same 404 as a missing node.
	return b.forgetIfConfirmedGone(id, b.client.RestoreVersion(context.Background(), id, version))
}
func (b *Browser) Shares(id string) (string, error) {
	out, err := b.client.Shares(context.Background(), id)
	if err != nil {
		return "", b.forgetIfGone(id, err)
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) CreateShare(id, email string, days int) (string, error) {
	out, err := b.client.CreateShare(context.Background(), id, email, days)
	if err != nil {
		// An unknown recipient is answered with the same 404 as a missing node.
		return "", b.forgetIfConfirmedGone(id, err)
	}
	data, err := json.Marshal(out)
	return string(data), err
}
func (b *Browser) RevokeShare(id string) error { return b.client.RevokeShare(context.Background(), id) }

// TrashBlockedMarker starts the error text when the server kept part of the trash (409): a
// trashed folder still holds an item that is not in the trash, so it and what it holds stay.
// Everything else asked for was removed. gomobile flattens errors to their text, so the app
// matches on it and explains it in the user's language.
const TrashBlockedMarker = "trash kept: a folder still holds items that are not in the trash"

func trashBlocked(err error) error {
	if protocol.StatusCode(err) == http.StatusConflict {
		return fmt.Errorf("%s (%v)", TrashBlockedMarker, err)
	}
	return err
}
