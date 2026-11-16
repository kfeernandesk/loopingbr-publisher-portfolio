//go:build !windows

package main

import "errors"

func protectSecret(s string) ([]byte, error) {
	return nil, errors.New("DPAPI is only available on Windows")
}
func unprotectSecret(data []byte) ([]byte, error) {
	return nil, errors.New("DPAPI is only available on Windows")
}
