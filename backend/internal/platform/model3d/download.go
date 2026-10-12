package model3d

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// NewDownloadClient 禁止图片和产物下载访问内网地址。
func NewDownloadClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	dialer.Control = func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("unresolved asset host %q", host)
		}
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("blocked asset address %s", ip)
		}
		return nil
	}
	return &http.Client{
		Timeout:   10 * time.Minute,
		Transport: &http.Transport{DialContext: dialer.DialContext},
	}
}
