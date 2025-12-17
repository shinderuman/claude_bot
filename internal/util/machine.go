package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// GetPeerAuthKey generates a unique authentication key based on the machine's identity.
// It uses Hostname + MachineID to generate a SHA256 hash.
func GetPeerAuthKey() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("failed to get hostname: %w", err)
	}

	machineID, err := GetMachineID()
	if err != nil {
		return "", fmt.Errorf("failed to get machine ID: %w", err)
	}

	data := hostname + machineID
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:]), nil
}

// GetMachineID retrieves the unique machine ID based on the OS.
func GetMachineID() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		// macOS: use IOPlatformUUID
		out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		if err != nil {
			return "", err
		}
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			if strings.Contains(line, "IOPlatformUUID") {
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					return strings.Trim(parts[1], " \""), nil
				}
			}
		}
		return "", fmt.Errorf("IOPlatformUUID not found")
	case "linux":
		// Linux: use /etc/machine-id or /var/lib/dbus/machine-id
		content, err := os.ReadFile("/etc/machine-id")
		if err == nil {
			return strings.TrimSpace(string(content)), nil
		}
		content, err = os.ReadFile("/var/lib/dbus/machine-id")
		if err == nil {
			return strings.TrimSpace(string(content)), nil
		}
		return "", fmt.Errorf("machine-id file not found")
	default:
		return "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}
