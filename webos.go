package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// registerBase is the webOS pairing handshake. Without a client-key the TV
// shows a one-time pairing prompt and returns a key; with one it authorises
// silently.
const registerBase = `{"type":"register","id":"register_0","payload":{"forcePairing":false,"pairingType":"PROMPT","manifest":{"manifestVersion":1,"appVersion":"1.0","signed":{"created":"20260427","appId":"com.lgctl.app","localizedAppNames":{"":"lgctl"},"localizedVendorNames":{"":"lgctl"},"permissions":["TEST_SECURE","CONTROL_INPUT_TEXT","CONTROL_MOUSE_AND_KEYBOARD","READ_INSTALLED_APPS","READ_LGE_SDX","READ_NOTIFICATIONS","SEARCH","WRITE_SETTINGS","WRITE_NOTIFICATION_ALERT","CONTROL_POWER","READ_CURRENT_CHANNEL","READ_RUNNING_APPS","READ_UPDATE_INFO","UPDATE_FROM_REMOTE_APP","READ_LGE_TV_INPUT_EVENTS","READ_TV_CURRENT_TIME"],"serial":"lgctl-webos"},"permissions":["LAUNCH","LAUNCH_WEBAPP","APP_TO_APP","CLOSE","TEST_OPEN","TEST_PROTECTED","CONTROL_AUDIO","CONTROL_DISPLAY","CONTROL_INPUT_JOYSTICK","CONTROL_INPUT_MEDIA_RECORDING","CONTROL_INPUT_MEDIA_PLAYBACK","CONTROL_INPUT_TV","CONTROL_POWER","CONTROL_TV_SCREEN","READ_APP_STATUS","READ_CURRENT_CHANNEL","READ_INPUT_DEVICE_LIST","READ_NETWORK_STATE","READ_RUNNING_APPS","READ_TV_CHANNEL_LIST","WRITE_NOTIFICATION_TOAST","READ_POWER_STATE","READ_COUNTRY_INFO","READ_SETTINGS"]}}}`

func buildRegister(clientKey string) string {
	if clientKey == "" {
		return registerBase
	}
	return strings.Replace(registerBase,
		`"pairingType":"PROMPT",`,
		`"pairingType":"PROMPT","client-key":"`+clientKey+`",`, 1)
}

// TV is an authenticated connection to a single webOS device.
type TV struct {
	cfg    *Config
	ws     *wsConn
	newKey bool // a fresh pairing key was received and should be persisted
}

// Connect dials the TV and completes the register handshake. regTimeout bounds
// how long to wait for the pairing reply (longer when a prompt may be shown).
func Connect(cfg *Config, dialTimeout, regTimeout time.Duration) (*TV, error) {
	ws, err := wsDial(cfg.IP, cfg.Port(), cfg.UseSSL(), dialTimeout)
	if err != nil {
		return nil, err
	}
	tv := &TV{cfg: cfg, ws: ws}
	if err := tv.register(regTimeout); err != nil {
		ws.Close()
		return nil, err
	}
	return tv, nil
}

func (t *TV) register(timeout time.Duration) error {
	if err := t.ws.writeText(buildRegister(t.cfg.ClientKey)); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out waiting for pairing")
		}
		t.ws.setReadDeadline(remaining)
		raw, err := t.ws.readText()
		if err != nil {
			return err
		}
		var r struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Error   string `json:"error"`
			Payload struct {
				ClientKey string `json:"client-key"`
			} `json:"payload"`
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		if r.ID != "register_0" {
			continue
		}
		switch r.Type {
		case "registered":
			if r.Payload.ClientKey != "" && r.Payload.ClientKey != t.cfg.ClientKey {
				t.cfg.ClientKey = r.Payload.ClientKey
				t.newKey = true
			}
			return nil
		case "response":
			// Intermediate ack while the pairing prompt is on screen; keep waiting.
			continue
		case "error":
			if r.Error != "" {
				return fmt.Errorf("pairing rejected: %s", r.Error)
			}
			return fmt.Errorf("pairing rejected or cancelled on the TV")
		}
	}
}

// request sends an ssap request and waits for the response with a matching id.
func (t *TV) request(id, uri string, payload map[string]any) (map[string]json.RawMessage, error) {
	msg := map[string]any{"type": "request", "id": id, "uri": "ssap://" + uri}
	if payload != nil {
		msg["payload"] = payload
	} else {
		msg["payload"] = map[string]any{}
	}
	b, _ := json.Marshal(msg)
	if err := t.ws.writeText(string(b)); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, fmt.Errorf("timed out waiting for response to %q", id)
		}
		t.ws.setReadDeadline(remaining)
		raw, err := t.ws.readText()
		if err != nil {
			return nil, err
		}
		var r struct {
			ID      string                     `json:"id"`
			Type    string                     `json:"type"`
			Error   string                     `json:"error"`
			Payload map[string]json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal([]byte(raw), &r); err != nil {
			continue
		}
		if r.ID != id {
			continue
		}
		if r.Type == "error" {
			return r.Payload, fmt.Errorf("%s", r.Error)
		}
		return r.Payload, nil
	}
}

// PowerState reports the TV's state string and whether it is mid-transition.
func (t *TV) PowerState() (state string, processing bool, err error) {
	pl, err := t.request("getPowerState", "com.webos.service.tvpower/power/getPowerState", nil)
	if err != nil {
		return "", false, err
	}
	if raw, ok := pl["state"]; ok {
		_ = json.Unmarshal(raw, &state)
	}
	_, processing = pl["processing"]
	return state, processing, nil
}

// ForegroundApp returns the appId of the currently active source/app.
func (t *TV) ForegroundApp() (string, error) {
	pl, err := t.request("getForegroundApp", "com.webos.applicationManager/getForegroundAppInfo", nil)
	if err != nil {
		return "", err
	}
	var app string
	if raw, ok := pl["appId"]; ok {
		_ = json.Unmarshal(raw, &app)
	}
	return app, nil
}

func (t *TV) TurnOnScreen() error {
	_, err := t.request("unblankScreen", "com.webos.service.tvpower/power/turnOnScreen", nil)
	return err
}

func (t *TV) PowerToggle() error {
	_, err := t.request("powerToggle", "system/turnOff", nil)
	return err
}

func (t *TV) TurnOff() error {
	_, err := t.request("turnOff", "system/turnOff", nil)
	return err
}

func (t *TV) SetHDMIInput(n int) error {
	_, err := t.request("launch", "system.launcher/launch",
		map[string]any{"id": fmt.Sprintf("com.webos.app.hdmi%d", n)})
	return err
}

// finish persists a new pairing key (if one was received) and closes the socket.
func (t *TV) finish() {
	if t.newKey {
		if err := t.cfg.Save(); err != nil {
			logf("warning: failed to save client_key: %v", err)
		} else {
			logf("saved new client_key to %s", t.cfg.path)
		}
	}
	t.ws.Close()
}
