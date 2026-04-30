package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelayPublicBaseURL(t *testing.T) {
	got := relayPublicBaseURL("http://relay.example.test/", "Alex Mac!")
	want := "http://relay.example.test/alexmac/v1"
	if got != want {
		t.Fatalf("relayPublicBaseURL = %q, want %q", got, want)
	}
}

func TestPrintPublishSummary(t *testing.T) {
	var buf bytes.Buffer
	printPublishSummary(&buf, publishSummary{
		RelayURL:         "http://127.0.0.1:8787",
		Slug:             "demo",
		Target:           "http://127.0.0.1:7341",
		DeviceID:         "od_test",
		OutpostAPIKey:    "op_test",
		PublicToken:      "relay-token",
		PublicAuthHeader: "X-Outpost-Relay-Token",
		ServerStarted:    true,
		CreatedAPIKey:    true,
	})

	got := buf.String()
	for _, want := range []string{
		"OpenAI base URL: http://127.0.0.1:8787/demo/v1",
		"X-Outpost-Relay-Token: Bearer relay-token",
		"Authorization: Bearer op_test",
		"Local server: started by publish",
		"shown once",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("publish summary missing %q in:\n%s", want, got)
		}
	}
}

func TestPrepareHostedRelayBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	result, err := prepareHostedRelayBundle(hostedRelayPrepareOptions{
		Dir:              dir,
		RelayURL:         "https://relay.example.test/",
		Slug:             "Alex Mac!",
		DeviceID:         "od_testdevice",
		AgentToken:       "ort_test",
		PublicToken:      "orp_public",
		PublicAuthHeader: "X-Outpost-Relay-Token",
		SourceDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.BaseURL != "https://relay.example.test/alexmac/v1" {
		t.Fatalf("BaseURL = %q", result.BaseURL)
	}
	if result.AgentToken != "ort_test" || result.PublicToken != "orp_public" {
		t.Fatalf("tokens were not preserved: %#v", result)
	}
	for name, path := range result.Files {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s file missing at %s: %v", name, path, err)
		}
	}

	envData, err := os.ReadFile(result.Files["env"])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(envData), "OUTPOST_RELAY_ENDPOINTS_B64=") {
		t.Fatalf("relay env missing encoded registry:\n%s", envData)
	}
	if strings.Contains(string(envData), "orp_public") {
		t.Fatalf("relay env should not contain client public token:\n%s", envData)
	}
}

func TestLoadRelayReservationsFromEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bundle")
	result, err := prepareHostedRelayBundle(hostedRelayPrepareOptions{
		Dir:              dir,
		RelayURL:         "https://relay.example.test",
		Slug:             "demo",
		DeviceID:         "od_testdevice",
		AgentToken:       "ort_test",
		PublicToken:      "orp_public",
		PublicAuthHeader: "X-Outpost-Relay-Token",
		SourceDir:        t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	envData, err := os.ReadFile(result.Files["env"])
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(envData), "\n") {
		if value, ok := strings.CutPrefix(line, "OUTPOST_RELAY_ENDPOINTS_B64="); ok {
			t.Setenv("OUTPOST_RELAY_ENDPOINTS_B64", value)
			break
		}
	}

	reservations, source, err := loadRelayReservations(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	reservation, ok := reservations["demo"]
	if !ok {
		t.Fatalf("demo reservation missing from %#v", reservations)
	}
	if reservation.DeviceID != "od_testdevice" || reservation.PublicTokenHash == "" {
		t.Fatalf("reservation not loaded from env: %#v", reservation)
	}
	if !strings.Contains(source, "OUTPOST_RELAY_ENDPOINTS_B64") {
		t.Fatalf("source did not mention env registry: %s", source)
	}
}
