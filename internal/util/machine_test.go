package util

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

func TestGetMachineID(t *testing.T) {
	id, err := GetMachineID()
	if err != nil {
		t.Fatalf("GetMachineID failed: %v", err)
	}
	if id == "" {
		t.Fatal("GetMachineID returned empty string")
	}
	t.Logf("Machine ID: %s", id)
}

func TestGetPeerAuthKey(t *testing.T) {
	key, err := GetPeerAuthKey()
	if err != nil {
		t.Fatalf("GetPeerAuthKey failed: %v", err)
	}
	if key == "" {
		t.Fatal("GetPeerAuthKey returned empty string")
	}

	hostname, _ := os.Hostname()
	machineID, _ := GetMachineID()
	expectedHash := sha256.Sum256([]byte(hostname + machineID))
	expectedKey := hex.EncodeToString(expectedHash[:])

	if key != expectedKey {
		t.Errorf("GetPeerAuthKey returned %s, expected %s", key, expectedKey)
	}
	t.Logf("Peer Auth Key: %s", key)
}
