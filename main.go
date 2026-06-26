package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"
)

var version = "0.1.0"

func logf(format string, args ...any) {
	log.Printf(format, args...)
}

const usage = `lgctl - control an LG webOS TV from Linux (wake/sleep with your PC)

Usage:
  lgctl [--config PATH] <command> [args]

Commands:
  pair             Pair with the TV (accept the prompt on screen). Saves the key.
  on               Wake the TV (Wake-on-LAN + power on) and optionally switch input.
  off [--force]    Power the TV off. Without --force this only happens if the PC's
                   HDMI input is the active source. Aliases of on/off: resume/suspend.
  input <1-4>      Switch the TV to the given HDMI input (waking it first if off).
  input-type <1-4> <type>
                   Set the input type (icon/label) for an HDMI input, e.g.
                   "pc", "gameconsole", "settopbox". Run with no type to list.
  status           Print the TV's power state and active input.
  version          Print version.

Config is searched in this order when --config is omitted:
  $LGCTL_CONFIG, /etc/lgctl/config.json, ~/.config/lgctl/config.json
`

func main() {
	log.SetFlags(0)
	log.SetPrefix("lgctl: ")

	fs := flag.NewFlagSet("lgctl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", "", "path to config file")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd := args[0]
	rest := args[1:]

	if cmd == "version" {
		fmt.Println("lgctl", version)
		return
	}
	if cmd == "help" || cmd == "-h" || cmd == "--help" {
		fmt.Print(usage)
		return
	}

	switch cmd {
	case "pair", "on", "resume", "wake", "off", "suspend", "sleep", "input", "input-type", "status":
		// known; fall through to load config below
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	switch cmd {
	case "pair":
		err = cmdPair(cfg)
	case "on", "resume", "wake":
		err = cmdOn(cfg)
	case "off", "suspend", "sleep":
		force := false
		offFs := flag.NewFlagSet("off", flag.ContinueOnError)
		offFs.BoolVar(&force, "force", false, "power off regardless of active input")
		if err := offFs.Parse(rest); err != nil {
			os.Exit(2)
		}
		err = cmdOff(cfg, force)
	case "input":
		if len(rest) != 1 {
			log.Fatal("usage: lgctl input <1-4>")
		}
		n, convErr := strconv.Atoi(rest[0])
		if convErr != nil || n < 1 {
			log.Fatalf("invalid HDMI input %q", rest[0])
		}
		err = cmdInput(cfg, n)
	case "input-type":
		if len(rest) == 1 && (rest[0] == "list" || rest[0] == "-h" || rest[0] == "--help") {
			printInputTypes()
			return
		}
		if len(rest) != 2 {
			printInputTypes()
			log.Fatal("usage: lgctl input-type <1-4> <type>")
		}
		n, convErr := strconv.Atoi(rest[0])
		if convErr != nil || n < 1 {
			log.Fatalf("invalid HDMI input %q", rest[0])
		}
		err = cmdInputType(cfg, n, rest[1])
	case "status":
		err = cmdStatus(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", cmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	if err != nil {
		log.Fatal(err)
	}
}

func cmdPair(cfg *Config) error {
	fmt.Println("Connecting... accept the pairing prompt on the TV with your remote.")
	tv, err := Connect(cfg, 5*time.Second, 90*time.Second)
	if err != nil {
		return err
	}
	defer tv.ws.Close()
	switch {
	case tv.newKey:
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("Paired. client_key saved to %s\n", cfg.path)
	case cfg.ClientKey != "":
		fmt.Println("Already paired (existing client_key is valid).")
	default:
		fmt.Println("Registered, but the TV returned no client key.")
	}

	if err := tv.EnableWOL(); err != nil {
		fmt.Printf("Note: could not auto-enable Wake-on-LAN (%v).\n"+
			"Enable it manually in the TV settings if waking doesn't work.\n", err)
	} else {
		fmt.Println("Wake-on-LAN enabled on the TV.")
	}
	return nil
}

// wake brings the TV up via Wake-on-LAN and waits for it to reach the Active
// state, returning a connected session. woke reports whether the TV actually
// had to be powered on (false if it was already Active), so callers can decide
// whether the post-wake input-switch delay is needed.
func wake(cfg *Config) (tv *TV, woke bool, err error) {
	deadline := time.Now().Add(time.Duration(cfg.Timeout()) * time.Second)

	stop := make(chan struct{})
	go wolLoop(cfg, stop)

	for {
		t, connErr := Connect(cfg, 3*time.Second, 10*time.Second)
		if connErr == nil {
			tv = t
			break
		}
		if time.Now().After(deadline) {
			close(stop)
			return nil, false, fmt.Errorf("power on: could not reach TV within %ds: %w", cfg.Timeout(), connErr)
		}
		time.Sleep(time.Second)
	}
	close(stop) // connected; stop hammering WOL

	for {
		state, processing, stErr := tv.PowerState()
		if stErr != nil {
			tv.finish()
			return nil, false, stErr
		}
		if state == "Active" && !processing {
			logf("TV is on")
			break
		}
		woke = true
		switch {
		case state == "Screen Off":
			logf("screen was off; turning it on")
			_ = tv.TurnOnScreen()
		case state == "Active Standby" && !processing:
			logf("TV in standby; toggling power")
			_ = tv.PowerToggle()
		default:
			logf("waiting for TV (state: %s)", state)
		}
		if time.Now().After(deadline) {
			logf("gave up waiting for Active state (last: %s)", state)
			break
		}
		time.Sleep(time.Second)
	}
	return tv, woke, nil
}

func cmdOn(cfg *Config) error {
	tv, woke, err := wake(cfg)
	if err != nil {
		return err
	}
	defer tv.finish()

	if cfg.SetInputOnWake {
		if woke {
			time.Sleep(time.Duration(cfg.InputWakeDelay()) * time.Second)
		}
		if err := tv.SetHDMIInput(cfg.HDMIInputOr1()); err != nil {
			logf("set input failed: %v", err)
		} else {
			logf("switched to HDMI %d", cfg.HDMIInputOr1())
		}
	}
	return nil
}

func cmdOff(cfg *Config, force bool) error {
	tv, err := Connect(cfg, 2*time.Second, 8*time.Second)
	if err != nil {
		// Most commonly the TV is already off and unreachable; not an error.
		logf("could not connect (TV likely already off): %v", err)
		return nil
	}
	defer tv.finish()

	state, _, err := tv.PowerState()
	if err != nil {
		return err
	}
	if state != "Active" && state != "Screen Off" {
		logf("TV already off (state: %s)", state)
		return nil
	}

	if cfg.CheckInputOnOff && !force {
		app, err := tv.ForegroundApp()
		if err != nil {
			return err
		}
		want := fmt.Sprintf("com.webos.app.hdmi%d", cfg.HDMIInputOr1())
		if app != want {
			logf("active input is %q, not the PC (%s); leaving TV on", app, want)
			return nil
		}
		logf("PC is the active input; powering off")
	}

	if err := tv.TurnOff(); err != nil {
		return err
	}
	logf("TV powered off")
	return nil
}

func cmdInput(cfg *Config, n int) error {
	tv, woke, err := wake(cfg)
	if err != nil {
		return err
	}
	defer tv.finish()
	if woke {
		time.Sleep(time.Duration(cfg.InputWakeDelay()) * time.Second)
	}
	if err := tv.SetHDMIInput(n); err != nil {
		return err
	}
	logf("switched to HDMI %d", n)
	return nil
}

func cmdInputType(cfg *Config, n int, name string) error {
	key, label, ok := lookupInputType(name)
	if !ok {
		printInputTypes()
		return fmt.Errorf("unknown input type %q", name)
	}
	tv, err := Connect(cfg, 3*time.Second, 10*time.Second)
	if err != nil {
		return err
	}
	defer tv.finish()
	id := fmt.Sprintf("HDMI_%d", n)
	if err := tv.SetDeviceConfig(id, key, label); err != nil {
		return err
	}
	logf("set %s input type to %s (%s)", id, label, key)
	return nil
}

func printInputTypes() {
	fmt.Fprintln(os.Stderr, "Available input types:")
	for _, it := range inputTypes {
		fmt.Fprintf(os.Stderr, "  %-13s %s\n", it.Key, it.Label)
	}
}

func cmdStatus(cfg *Config) error {
	tv, err := Connect(cfg, 3*time.Second, 10*time.Second)
	if err != nil {
		return err
	}
	defer tv.finish()
	state, processing, err := tv.PowerState()
	if err != nil {
		return err
	}
	app, _ := tv.ForegroundApp()
	fmt.Printf("Power: %s%s\n", state, map[bool]string{true: " (transitioning)", false: ""}[processing])
	if app != "" {
		fmt.Printf("Active app/input: %s\n", app)
	}
	return nil
}
