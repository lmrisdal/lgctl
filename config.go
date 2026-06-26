package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	IP        string  `json:"ip"`
	MAC       MACList `json:"mac"`
	ClientKey string  `json:"client_key,omitempty"`

	// SSL selects the encrypted websocket endpoint (port 3001). Defaults to true.
	SSL *bool `json:"ssl,omitempty"`

	// HDMIInput is the HDMI port the PC is connected to (1-4). Defaults to 1.
	HDMIInput int `json:"hdmi_input,omitempty"`

	// CheckInputOnOff: only power the TV off if the PC's HDMI input is the
	// currently active source. Mirrors the Windows app's behaviour.
	CheckInputOnOff bool `json:"check_input_on_off,omitempty"`

	// SetInputOnWake: switch the TV to the PC's HDMI input after waking it.
	SetInputOnWake bool `json:"set_input_on_wake,omitempty"`

	// InputWakeDelaySec: seconds to wait after power-on before switching input.
	InputWakeDelaySec int `json:"input_wake_delay_sec,omitempty"`

	// TimeoutSec: how long to keep retrying a power-on (WOL + connect).
	TimeoutSec int `json:"timeout_sec,omitempty"`

	// Subnet mask used to compute the directed broadcast address for WOL.
	Subnet string `json:"subnet,omitempty"`

	path string // source file, not serialized
}

// MACList accepts either a single "aa:bb:.." string or a JSON array of strings.
type MACList []string

func (m *MACList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		var out MACList
		for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
			if part != "" {
				out = append(out, part)
			}
		}
		*m = out
		return nil
	}
	var arr []string
	if err := json.Unmarshal(b, &arr); err != nil {
		return fmt.Errorf("mac must be a string or array of strings: %w", err)
	}
	*m = arr
	return nil
}

func (c *Config) UseSSL() bool {
	if c.SSL == nil {
		return true
	}
	return *c.SSL
}

func (c *Config) Port() int {
	if c.UseSSL() {
		return 3001
	}
	return 3000
}

func (c *Config) HDMIInputOr1() int {
	if c.HDMIInput < 1 {
		return 1
	}
	return c.HDMIInput
}

func (c *Config) InputWakeDelay() int {
	if c.InputWakeDelaySec <= 0 {
		return 1
	}
	return c.InputWakeDelaySec
}

func (c *Config) Timeout() int {
	if c.TimeoutSec <= 0 {
		return 20
	}
	return c.TimeoutSec
}

func (c *Config) SubnetMask() string {
	if c.Subnet == "" {
		return "255.255.255.0"
	}
	return c.Subnet
}

func configSearchPaths() []string {
	var paths []string
	if env := os.Getenv("LGCTL_CONFIG"); env != "" {
		paths = append(paths, env)
	}
	paths = append(paths, "/etc/lgctl/config.json")
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "lgctl", "config.json"))
	}
	return paths
}

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		for _, p := range configSearchPaths() {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			return nil, fmt.Errorf("no config file found; checked %s", strings.Join(configSearchPaths(), ", "))
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if c.IP == "" {
		return nil, fmt.Errorf("config %s: \"ip\" is required", path)
	}
	c.path = path
	return &c, nil
}

func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config has no source path to save to")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}
