//go:build windows

package main

import (
	"errors"
	"syscall"
	"unsafe"
)

// Windows Data Protection API protects the GitHub token for the current user.
type dataBlob struct {
	Length uint32
	Data   *byte
}

var crypt32 = syscall.NewLazyDLL("crypt32.dll")
var kernel32 = syscall.NewLazyDLL("kernel32.dll")
var dpapiProtect = crypt32.NewProc("CryptProtectData")
var dpapiUnprotect = crypt32.NewProc("CryptUnprotectData")
var localFree = kernel32.NewProc("LocalFree")

func doDPAPI(data []byte, protect bool) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("empty DPAPI buffer")
	}
	input := dataBlob{Length: uint32(len(data)), Data: &data[0]}
	var output dataBlob
	proc := dpapiUnprotect
	if protect {
		proc = dpapiProtect
	}
	ok, _, err := proc.Call(uintptr(unsafe.Pointer(&input)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&output)))
	if ok == 0 {
		return nil, fmtDPAPIError(err)
	}
	defer localFree.Call(uintptr(unsafe.Pointer(output.Data)))
	out := append([]byte(nil), unsafe.Slice(output.Data, int(output.Length))...)
	return out, nil
}
func fmtDPAPIError(err error) error {
	if err == nil {
		return errors.New("DPAPI failed")
	}
	return err
}
func protectSecret(data string) ([]byte, error)   { return doDPAPI([]byte(data), true) }
func unprotectSecret(data []byte) ([]byte, error) { return doDPAPI(data, false) }
