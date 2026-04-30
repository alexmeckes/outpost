package outpost

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRelayRegistryCreateEndpointStoresHashedPublicToken(t *testing.T) {
	registry := &RelayRegistry{}
	endpoint, token, err := registry.CreateEndpoint(RelayEndpointCreateOptions{
		Slug:        "Demo Endpoint!",
		DeviceID:    "OD_TestDevice",
		PublicToken: "auto",
	})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.Slug != "demoendpoint" {
		t.Fatalf("slug = %q, want demoendpoint", endpoint.Slug)
	}
	if endpoint.DeviceID != "od_testdevice" {
		t.Fatalf("device = %q, want od_testdevice", endpoint.DeviceID)
	}
	if token == "" || !strings.HasPrefix(token, "orp_") {
		t.Fatalf("token = %q, want orp_ token", token)
	}
	if endpoint.PublicTokenHash == "" || endpoint.PublicTokenHash == token {
		t.Fatalf("public token hash was not stored safely: %q", endpoint.PublicTokenHash)
	}
	if endpoint.PublicTokenPrefix == "" || !strings.HasPrefix(token, endpoint.PublicTokenPrefix) {
		t.Fatalf("prefix = %q, token = %q", endpoint.PublicTokenPrefix, token)
	}

	reservation := registry.Reservations()[endpoint.Slug]
	if reservation.PublicTokenHash != endpoint.PublicTokenHash {
		t.Fatalf("reservation token hash = %q, want %q", reservation.PublicTokenHash, endpoint.PublicTokenHash)
	}
}

func TestRelayRegistryRejectsDuplicateAndCanReplace(t *testing.T) {
	registry := &RelayRegistry{}
	if _, _, err := registry.CreateEndpoint(RelayEndpointCreateOptions{
		Slug:        "demo",
		DeviceID:    "od_first",
		PublicToken: "none",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.CreateEndpoint(RelayEndpointCreateOptions{
		Slug:        "demo",
		DeviceID:    "od_second",
		PublicToken: "none",
	}); err == nil {
		t.Fatal("duplicate endpoint was accepted without --replace")
	}
	endpoint, _, err := registry.CreateEndpoint(RelayEndpointCreateOptions{
		Slug:        "demo",
		DeviceID:    "od_second",
		PublicToken: "none",
		Replace:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if endpoint.DeviceID != "od_second" {
		t.Fatalf("device = %q, want od_second", endpoint.DeviceID)
	}
	active := registry.ActiveEndpoints()
	if len(active) != 1 || active[0].DeviceID != "od_second" {
		t.Fatalf("active endpoints = %+v", active)
	}
}

func TestRelayRegistrySaveLoadAndRevoke(t *testing.T) {
	path := filepath.Join(t.TempDir(), "relay.json")
	registry := &RelayRegistry{}
	if _, _, err := registry.CreateEndpoint(RelayEndpointCreateOptions{
		Slug:        "demo",
		DeviceID:    "od_test",
		PublicToken: "none",
	}); err != nil {
		t.Fatal(err)
	}
	if err := SaveRelayRegistry(path, registry); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadRelayRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Registry.ActiveEndpoint("demo"); !ok {
		t.Fatal("saved endpoint was not loaded")
	}
	if !loaded.Registry.RevokeEndpoint("demo") {
		t.Fatal("endpoint was not revoked")
	}
	if err := SaveRelayRegistry(path, loaded.Registry); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadRelayRegistry(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Registry.ActiveEndpoints()) != 0 {
		t.Fatalf("active endpoints after revoke = %+v", reloaded.Registry.ActiveEndpoints())
	}
}
