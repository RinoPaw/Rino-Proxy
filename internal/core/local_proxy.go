package core

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ValidateLocalProxy permits loopback and exact IP addresses assigned to an
// interface of THIS machine. OpenWrt often binds its mixed-port to br-lan,
// such as 172.27.207.1, rather than 127.0.0.1. Remote Tailnet/LAN
// endpoints and DNS names are never implicitly trusted as local.
func ValidateLocalProxy(address string) error {
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return errors.New("cannot inspect host network interfaces")
	}
	var local []net.IP
	for _, item := range interfaces {
		switch v := item.(type) {
		case *net.IPNet:
			local = append(local, v.IP)
		case *net.IPAddr:
			local = append(local, v.IP)
		}
	}
	return validateLocalProxy(address, local)
}

func validateLocalProxy(address string, local []net.IP) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" ||
		(u.Path != "" && u.Path != "/") || strings.TrimSpace(u.Host) == "" {
		return errors.New("proxy must be a local HTTP URL without credentials, query or path")
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil {
		return errors.New("proxy must use a literal IP address")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return errors.New("proxy must specify a valid TCP port")
	}
	if ip.IsUnspecified() || ip.IsMulticast() {
		return errors.New("proxy cannot use a wildcard or multicast address")
	}
	if ip.IsLoopback() {
		return nil
	}
	for _, own := range local {
		if ip.Equal(own) {
			return nil
		}
	}
	return fmt.Errorf("proxy %s must be an address assigned to this host", ip)
}
