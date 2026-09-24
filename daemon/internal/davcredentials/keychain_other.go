//go:build !darwin || !cgo

package davcredentials

import "errors"

func Load(string) (string, error) { return "", errors.New("Apple account setup is unavailable") }
func Save(string, string) error   { return errors.New("Apple account setup is unavailable") }
func Delete(string) error         { return errors.New("Apple account setup is unavailable") }
