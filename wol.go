package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"
)

func magicPacket(mac string) ([]byte, error) {
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ':', '-', '.', ' ':
			return -1
		}
		return r
	}, mac)
	hw, err := hex.DecodeString(clean)
	if err != nil || len(hw) != 6 {
		return nil, fmt.Errorf("invalid MAC address %q", mac)
	}
	pkt := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		pkt = append(pkt, 0xFF)
	}
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}
	return pkt, nil
}

// wolTargets returns the addresses a magic packet is sent to: the global
// broadcast, the directed subnet broadcast, and the TV's own IP (helps when the
// ARP entry is still warm).
func wolTargets(cfg *Config) []string {
	targets := []string{"255.255.255.255"}
	if b := subnetBroadcast(cfg.IP, cfg.SubnetMask()); b != "" && b != "255.255.255.255" {
		targets = append(targets, b)
	}
	targets = append(targets, cfg.IP)
	return targets
}

func subnetBroadcast(ip, mask string) string {
	ipp := net.ParseIP(ip).To4()
	mp := net.ParseIP(mask).To4()
	if ipp == nil || mp == nil {
		return ""
	}
	b := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		b[i] = ipp[i] | ^mp[i]
	}
	return b.String()
}

func sendWOL(cfg *Config) error {
	if len(cfg.MAC) == 0 {
		return fmt.Errorf("no MAC address configured for WOL")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return err
	}
	defer conn.Close()

	if sc, err := conn.SyscallConn(); err == nil {
		_ = sc.Control(func(fd uintptr) {
			_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
	}

	targets := wolTargets(cfg)
	for _, mac := range cfg.MAC {
		pkt, err := magicPacket(mac)
		if err != nil {
			logf("%v", err)
			continue
		}
		for _, tgt := range targets {
			addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(tgt, "9"))
			if err != nil {
				continue
			}
			_, _ = conn.WriteToUDP(pkt, addr)
		}
	}
	return nil
}

func wolLoop(cfg *Config, stop <-chan struct{}) {
	if err := sendWOL(cfg); err != nil {
		logf("WOL: %v", err)
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			_ = sendWOL(cfg)
		}
	}
}
