//go:build !linux

package hostif

import "net"

func Lookup(name string) (*net.Interface, error) {
	return net.InterfaceByName(name)
}

func Present(name string) bool {
	_, err := net.InterfaceByName(name)
	return err == nil
}
