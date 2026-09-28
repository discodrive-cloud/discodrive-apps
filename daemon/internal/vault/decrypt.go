// Package vault — decrypt.go: traversal of an encrypted Cryptomator vault tree.
package vault

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DecryptTree decrypts the full Cryptomator vault tree from vaultDir into outDir.
// Starts from the root dirID = "" (empty string).
func (v *Vault) DecryptTree(vaultDir, outDir string) error {
	return v.decryptDir(vaultDir, outDir, "")
}

// decryptDir recursively decrypts the directory with the given dirID.
// storagePath = vaultDir + DirIdHash(dirID).
func (v *Vault) decryptDir(vaultDir, outDir, dirID string) error {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	// Compute the storage path for this directory
	hashPath, err := v.DirIdHash(dirID)
	if err != nil {
		return fmt.Errorf("vault: DirIdHash(%q): %w", dirID, err)
	}
	storagePath := filepath.Join(vaultDir, hashPath)

	entries, err := os.ReadDir(storagePath)
	if err != nil {
		return fmt.Errorf("vault: ReadDir %q: %w", storagePath, err)
	}

	for _, e := range entries {
		name := e.Name()

		// Skip dirid.c9r — it marks the current directory ID
		if name == "dirid.c9r" {
			continue
		}
		// A sync conflict copy of a ciphertext entry ("<enc> (conflict, …).c9r") is not
		// an encrypted name; skip it rather than fail the whole vault.
		if strings.Contains(name, " (conflict, ") {
			continue
		}

		if strings.HasSuffix(name, ".c9s") && e.IsDir() {
			// .c9s shortened name: read name.c9s → full encrypted name
			c9sPath := filepath.Join(storagePath, name)
			fullEncBytes, err := os.ReadFile(filepath.Join(c9sPath, "name.c9s"))
			if err != nil {
				return fmt.Errorf("vault: reading name.c9s in %q: %w", name, err)
			}
			fullEnc := string(fullEncBytes)

			plainName, err := v.DecryptName(fullEnc, dirID)
			if errors.Is(err, ErrInvalidName) {
				continue // hostile or corrupt entry: never let it name a local path
			}
			if err != nil {
				return fmt.Errorf("vault: decrypting name from name.c9s (%q): %w", fullEnc, err)
			}

			dirCPath := filepath.Join(c9sPath, "dir.c9r")
			contentsCPath := filepath.Join(c9sPath, "contents.c9r")

			if _, err := os.Stat(dirCPath); err == nil {
				// Subdirectory: read plaintext subDirID, recurse
				subDirIDBytes, err := os.ReadFile(dirCPath)
				if err != nil {
					return fmt.Errorf("vault: reading dir.c9r in .c9s %q: %w", name, err)
				}
				subOutDir := filepath.Join(outDir, plainName)
				if err := v.decryptDir(vaultDir, subOutDir, string(subDirIDBytes)); err != nil {
					return err
				}
			} else if _, err := os.Stat(contentsCPath); err == nil {
				// File: decrypt contents.c9r
				outPath := filepath.Join(outDir, plainName)
				if err := decryptFile(v, contentsCPath, outPath); err != nil {
					return fmt.Errorf("vault: decrypting contents.c9r in .c9s %q: %w", name, err)
				}
			} else {
				return fmt.Errorf("vault: .c9s %q contains neither dir.c9r nor contents.c9r", name)
			}
			continue
		}

		if !strings.HasSuffix(name, ".c9r") {
			// Skip unrecognised files
			continue
		}

		if e.IsDir() {
			// Subdirectory: <encName>.c9r/ contains dir.c9r with plaintext subDirID
			plainName, err := v.DecryptName(name, dirID)
			if errors.Is(err, ErrInvalidName) {
				continue // hostile or corrupt entry: never let it name a local path
			}
			if err != nil {
				return fmt.Errorf("vault: decrypting directory name %q: %w", name, err)
			}

			// Read subDirID from <entry>/dir.c9r. A Cryptomator symlink is also a
			// <name>.c9r folder, holding symlink.c9r instead: it is not materialised.
			dirCPath := filepath.Join(storagePath, name, "dir.c9r")
			if _, err := os.Stat(dirCPath); os.IsNotExist(err) {
				if _, serr := os.Stat(filepath.Join(storagePath, name, "symlink.c9r")); serr == nil {
					continue
				}
			}
			subDirIDBytes, err := os.ReadFile(dirCPath)
			if err != nil {
				return fmt.Errorf("vault: reading dir.c9r for %q: %w", name, err)
			}
			subDirID := string(subDirIDBytes)

			// Recurse
			subOutDir := filepath.Join(outDir, plainName)
			if err := v.decryptDir(vaultDir, subOutDir, subDirID); err != nil {
				return err
			}
		} else {
			// Encrypted file: <encName>.c9r
			plainName, err := v.DecryptName(name, dirID)
			if errors.Is(err, ErrInvalidName) {
				continue // hostile or corrupt entry: never let it name a local path
			}
			if err != nil {
				return fmt.Errorf("vault: decrypting file name %q: %w", name, err)
			}

			inPath := filepath.Join(storagePath, name)
			outPath := filepath.Join(outDir, plainName)
			if err := decryptFile(v, inPath, outPath); err != nil {
				return fmt.Errorf("vault: decrypting file %q → %q: %w", inPath, outPath, err)
			}
		}
	}

	return nil
}

func decryptFile(v *Vault, inPath, outPath string) error {
	in, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close()

	return v.DecryptContent(out, in)
}
