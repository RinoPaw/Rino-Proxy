package core

import (
	"net"
	"strings"
	"testing"
)

func TestValidateLocalProxyAddress(t *testing.T) {
	own := []net.IP{net.ParseIP("172.27.207.1"), net.ParseIP("100.64.0.2"), net.ParseIP("fe80::1")}
	accepted := []string{
		"http://127.0.0.1:7890",
		"http://[::1]:9090",
		"http://172.27.207.1:7890",
		"http://100.64.0.2:17890",
	}
	for _, address := range accepted {
		if err := validateLocalProxy(address, own); err != nil {
			t.Errorf("should accept own address %q: %v", address, err)
		}
	}
	rejected := []string{
		"http://172.27.207.2:7890",
		"http://100.64.0.3:17890",
		"http://8.8.8.8:80",
		"http://192.168.0.1:7890",
		"http://0.0.0.0:7890",
		"http://[::]:7890",
		"http://224.0.0.1:7890",
		"http://router.local:7890",
		"http://172.27.207.1",
		"http://172.27.207.1:0",
		"http://172.27.207.1:99999",
		"https://172.27.207.1:7890",
		"http://user:pass@172.27.207.1:7890",
		"http://172.27.207.1:7890/?secret=1",
		"http://172.27.207.1:7890/proxies",
		"http://172.27.207.1:7890/#test",
	}
	for _, address := range rejected {
		if err := validateLocalProxy(address, own); err == nil {
			t.Errorf("should reject remote or ambiguous address %q", address)
		}
	}
}

func TestValidateLocalProxyOnHost(t *testing.T) {
	if err := ValidateLocalProxy("http://127.0.0.1:7890"); err != nil {
		t.Fatalf("should allow loopback: %v", err)
	}
	if err := ValidateLocalProxy("http://203.0.113.17:7890"); err == nil || !strings.Contains(err.Error(), "assigned") {
		t.Fatalf("should refuse nonlocal documentation address: %v", err)
	}
}
