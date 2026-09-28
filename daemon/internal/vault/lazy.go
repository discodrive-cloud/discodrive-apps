// Package vault — lazy.go: on-demand (lazy) vault traversal over Source/Sink.
// Tree-walk logic mirrors decrypt.go/encrypt.go, but I/O goes through Source/Sink.
package vault

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/google/uuid"
)

// VEntry is a decrypted directory entry.
type VEntry struct {
	Name  string
	IsDir bool
	// DirID is the directory's own ID (for recursion) — set when IsDir.
	DirID string
	// FileStoragePath is the storage path of the file's encrypted contents
	// (pass to ReadFile) — set when !IsDir.
	FileStoragePath string
}

// OpenWithSource opens a vault by reading masterkey.cryptomator and
// vault.cryptomator through src. Wrong password → ErrWrongPassword.
func OpenWithSource(src Source, password string) (*Vault, error) {
	mkData, err := src.ReadFile("masterkey.cryptomator")
	if err != nil {
		return nil, fmt.Errorf("vault: reading masterkey.cryptomator: %w", err)
	}
	jwtData, err := src.ReadFile("vault.cryptomator")
	if err != nil {
		return nil, fmt.Errorf("vault: reading vault.cryptomator: %w", err)
	}
	return deriveVault(mkData, jwtData, password)
}

// ListDir lists one directory by its dirID (root = ""), decrypting child names.
// It does not recurse; for a child directory use its DirID.
func (v *Vault) ListDir(src Source, dirID string) ([]VEntry, error) {
	hashPath, err := v.DirIdHash(dirID)
	if err != nil {
		return nil, fmt.Errorf("vault: DirIdHash(%q): %w", dirID, err)
	}

	entries, err := src.ListDir(hashPath)
	if err != nil {
		return nil, fmt.Errorf("vault: ListDir %q: %w", hashPath, err)
	}

	var out []VEntry
	for _, e := range entries {
		name := e.Name

		// Skip dirid.c9r — it marks the current directory ID
		if name == "dirid.c9r" {
			continue
		}
		// A sync conflict copy of a ciphertext entry ("<enc> (conflict, …).c9r") is not
		// an encrypted name; skip it rather than fail the whole vault.
		if strings.Contains(name, " (conflict, ") {
			continue
		}

		if strings.HasSuffix(name, ".c9s") && e.IsDir {
			// .c9s shortened name: read name.c9s → full encrypted name
			c9sPath := path.Join(hashPath, name)
			fullEncBytes, err := src.ReadFile(path.Join(c9sPath, "name.c9s"))
			if err != nil {
				return nil, fmt.Errorf("vault: reading name.c9s in %q: %w", name, err)
			}
			plainName, err := v.DecryptName(string(fullEncBytes), dirID)
			if errors.Is(err, ErrInvalidName) {
				continue // hostile or corrupt entry: never let it name a local path
			}
			if err != nil {
				return nil, fmt.Errorf("vault: decrypting name from name.c9s (%q): %w", fullEncBytes, err)
			}

			sub, err := src.ListDir(c9sPath)
			if err != nil {
				return nil, fmt.Errorf("vault: ListDir %q: %w", c9sPath, err)
			}
			hasDir, hasContents := false, false
			for _, s := range sub {
				switch s.Name {
				case "dir.c9r":
					hasDir = true
				case "contents.c9r":
					hasContents = true
				}
			}

			if hasDir {
				subDirIDBytes, err := src.ReadFile(path.Join(c9sPath, "dir.c9r"))
				if err != nil {
					return nil, fmt.Errorf("vault: reading dir.c9r in .c9s %q: %w", name, err)
				}
				out = append(out, VEntry{Name: plainName, IsDir: true, DirID: string(subDirIDBytes)})
			} else if hasContents {
				out = append(out, VEntry{Name: plainName, IsDir: false, FileStoragePath: path.Join(c9sPath, "contents.c9r")})
			} else {
				return nil, fmt.Errorf("vault: .c9s %q contains neither dir.c9r nor contents.c9r", name)
			}
			continue
		}

		if !strings.HasSuffix(name, ".c9r") {
			// Skip unrecognised files
			continue
		}

		if e.IsDir {
			// Subdirectory: <encName>.c9r/ contains dir.c9r with plaintext subDirID
			plainName, err := v.DecryptName(name, dirID)
			if errors.Is(err, ErrInvalidName) {
				continue // hostile or corrupt entry: never let it name a local path
			}
			if err != nil {
				return nil, fmt.Errorf("vault: decrypting directory name %q: %w", name, err)
			}
			subDirIDBytes, err := src.ReadFile(path.Join(hashPath, name, "dir.c9r"))
			if err != nil {
				// A Cryptomator symlink is a <name>.c9r folder with symlink.c9r in it.
				if _, serr := src.ReadFile(path.Join(hashPath, name, "symlink.c9r")); serr == nil {
					continue
				}
				return nil, fmt.Errorf("vault: reading dir.c9r for %q: %w", name, err)
			}
			out = append(out, VEntry{Name: plainName, IsDir: true, DirID: string(subDirIDBytes)})
		} else {
			// Encrypted file: <encName>.c9r
			plainName, err := v.DecryptName(name, dirID)
			if errors.Is(err, ErrInvalidName) {
				continue // hostile or corrupt entry: never let it name a local path
			}
			if err != nil {
				return nil, fmt.Errorf("vault: decrypting file name %q: %w", name, err)
			}
			out = append(out, VEntry{Name: plainName, IsDir: false, FileStoragePath: path.Join(hashPath, name)})
		}
	}

	return out, nil
}

// ReadFile decrypts one file by its storage path (VEntry.FileStoragePath).
func (v *Vault) ReadFile(src Source, fileStoragePath string) ([]byte, error) {
	enc, err := src.ReadFile(fileStoragePath)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := v.DecryptContent(&out, bytes.NewReader(enc)); err != nil {
		return nil, fmt.Errorf("vault: decrypting %q: %w", fileStoragePath, err)
	}
	return out.Bytes(), nil
}

// WriteFile encrypts plaintext and writes file `name` into the directory parentDirID.
// Mirrors the file branch of encryptDir (including .c9s shortening).
func (v *Vault) WriteFile(sink Sink, parentDirID, name string, plaintext []byte) error {
	var buf bytes.Buffer
	if err := v.EncryptContent(&buf, bytes.NewReader(plaintext)); err != nil {
		return err
	}
	return v.WriteEncryptedFile(sink, parentDirID, name, &buf)
}

// WriteEncryptedFile stores content produced by EncryptContent without buffering
// another whole file when the sink supports streaming. Shortened names share the
// same layout as the byte-oriented API.
func (v *Vault) WriteEncryptedFile(sink Sink, parentDirID, name string, encrypted io.Reader) error {
	parentHash, err := v.DirIdHash(parentDirID)
	if err != nil {
		return err
	}
	encName, err := v.EncryptName(name, parentDirID)
	if err != nil {
		return err
	}
	write := func(sp string) error {
		if streaming, ok := sink.(interface{ WriteFileStream(string, io.Reader) error }); ok {
			return streaming.WriteFileStream(sp, encrypted)
		}
		data, err := io.ReadAll(encrypted)
		if err != nil {
			return err
		}
		return sink.WriteFile(sp, data)
	}

	if len(encName) > shorteningThreshold {
		shortened := shortenedName(encName)
		c9sPath := path.Join(parentHash, shortened)
		if err := sink.MakeDir(c9sPath); err != nil {
			return fmt.Errorf("vault: creating .c9s directory for %q: %w", name, err)
		}
		if err := sink.WriteFile(path.Join(c9sPath, "name.c9s"), []byte(encName)); err != nil {
			return fmt.Errorf("vault: writing name.c9s for %q: %w", name, err)
		}
		return write(path.Join(c9sPath, "contents.c9r"))
	}
	return write(path.Join(parentHash, encName))
}

// MakeDir creates subdirectory `name` in parentDirID and returns the new dirID.
// Mirrors the directory branch of encryptDir: writes dir.c9r in the parent, then
// creates the child storage directory with its dirid.c9r.
func (v *Vault) MakeDir(sink Sink, parentDirID, name string) (string, error) {
	parentHash, err := v.DirIdHash(parentDirID)
	if err != nil {
		return "", fmt.Errorf("vault: DirIdHash(%q): %w", parentDirID, err)
	}
	encName, err := v.EncryptName(name, parentDirID)
	if err != nil {
		return "", fmt.Errorf("vault: encrypting name %q: %w", name, err)
	}
	subDirID := uuid.NewString()

	if len(encName) > shorteningThreshold {
		shortened := shortenedName(encName)
		c9sPath := path.Join(parentHash, shortened)
		if err := sink.MakeDir(c9sPath); err != nil {
			return "", fmt.Errorf("vault: creating .c9s directory for %q: %w", name, err)
		}
		if err := sink.WriteFile(path.Join(c9sPath, "name.c9s"), []byte(encName)); err != nil {
			return "", fmt.Errorf("vault: writing name.c9s for %q: %w", name, err)
		}
		if err := sink.WriteFile(path.Join(c9sPath, "dir.c9r"), []byte(subDirID)); err != nil {
			return "", fmt.Errorf("vault: writing dir.c9r in .c9s for %q: %w", name, err)
		}
	} else {
		encDirPath := path.Join(parentHash, encName)
		if err := sink.MakeDir(encDirPath); err != nil {
			return "", fmt.Errorf("vault: creating directory for %q: %w", name, err)
		}
		if err := sink.WriteFile(path.Join(encDirPath, "dir.c9r"), []byte(subDirID)); err != nil {
			return "", fmt.Errorf("vault: writing dir.c9r for %q: %w", name, err)
		}
	}

	// Create the child storage directory and its dirid.c9r
	childHash, err := v.DirIdHash(subDirID)
	if err != nil {
		return "", fmt.Errorf("vault: DirIdHash(child %q): %w", subDirID, err)
	}
	if err := sink.MakeDir(childHash); err != nil {
		return "", fmt.Errorf("vault: creating child storage directory: %w", err)
	}
	if err := v.writeDirIDFileSink(sink, childHash, subDirID); err != nil {
		return "", fmt.Errorf("vault: writing dirid.c9r for child: %w", err)
	}
	return subDirID, nil
}

// Remove deletes file/dir `name` from parentDirID. Because EncryptName and
// DirIdHash are deterministic, the on-disk entry name is recomputed directly.
// (Removing a subdirectory leaves its child storage dir orphaned — out of scope;
// Cryptomator handles such orphans via GC.)
func (v *Vault) Remove(sink Sink, parentDirID, name string) error {
	parentHash, err := v.DirIdHash(parentDirID)
	if err != nil {
		return fmt.Errorf("vault: DirIdHash(%q): %w", parentDirID, err)
	}
	encName, err := v.EncryptName(name, parentDirID)
	if err != nil {
		return fmt.Errorf("vault: encrypting name %q: %w", name, err)
	}
	entry := encName
	if len(encName) > shorteningThreshold {
		entry = shortenedName(encName)
	}
	return sink.Remove(path.Join(parentHash, entry))
}

// writeDirIDFileSink writes dirid.c9r (encrypted dirID) into storagePath via sink.
func (v *Vault) writeDirIDFileSink(sink Sink, storagePath, dirID string) error {
	var buf bytes.Buffer
	if err := v.EncryptContent(&buf, strings.NewReader(dirID)); err != nil {
		return err
	}
	return sink.WriteFile(path.Join(storagePath, "dirid.c9r"), buf.Bytes())
}
