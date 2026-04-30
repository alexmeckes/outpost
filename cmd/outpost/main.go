package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"outpost/internal/outpost"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		args = []string{"start"}
	}

	switch args[0] {
	case "start":
		return start()
	case "status":
		return status()
	case "keys":
		return keys(args[1:])
	case "logs":
		return logs(args[1:])
	case "expose":
		return expose(args[1:])
	case "publish":
		return publish(args[1:])
	case "relay":
		return relay(args[1:])
	case "config":
		return config(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func start() error {
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	if loaded.Created {
		fmt.Println("Created Outpost config:", loaded.Path)
		fmt.Println("Initial API key, shown once:")
		fmt.Println(loaded.InitialKey)
		fmt.Println()
	}

	cfg := loaded.Config
	fmt.Printf("Outpost listening on http://%s\n", cfg.ListenAddr)
	fmt.Printf("OpenAI base URL: http://%s/v1\n", cfg.ListenAddr)
	fmt.Printf("Backend: %s at %s\n", cfg.Backend.Type, cfg.Backend.BaseURL)
	fmt.Printf("Config: %s\n", loaded.Path)
	fmt.Printf("Logs: %s\n", cfg.LogPath)

	httpServer := newOutpostHTTPServer(cfg)
	return httpServer.ListenAndServe()
}

func status() error {
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	cfg := loaded.Config

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	backendStatus, backendErr := outpost.CheckBackend(ctx, cfg.Backend)

	fmt.Println("Config:", loaded.Path)
	fmt.Println("Listen:", cfg.ListenAddr)
	fmt.Printf("Backend: %s at %s\n", cfg.Backend.Type, cfg.Backend.BaseURL)
	if backendErr != nil {
		fmt.Println("Backend status: unavailable:", backendErr)
	} else {
		fmt.Println("Backend status:", backendStatus)
	}
	fmt.Println("Active keys:", len(cfg.ActiveKeys()))
	fmt.Println("Log path:", cfg.LogPath)
	if loaded.Created {
		fmt.Println()
		fmt.Println("Created initial API key, shown once:")
		fmt.Println(loaded.InitialKey)
	}
	return nil
}

func keys(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: outpost keys <list|create|revoke>")
	}
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	cfg := loaded.Config

	switch args[0] {
	case "list":
		fmt.Println("ID\tPrefix\tName\tCreated\tRevoked\tRPM")
		for _, key := range cfg.APIKeys {
			revoked := "-"
			if key.RevokedAt != nil {
				revoked = key.RevokedAt.Format(time.RFC3339)
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%d\n",
				key.ID,
				key.Prefix,
				key.Name,
				key.CreatedAt.Format(time.RFC3339),
				revoked,
				key.RequestsPerMinute,
			)
		}
		return nil
	case "create":
		name := "default"
		if len(args) > 1 {
			name = strings.Join(args[1:], " ")
		}
		key, token, err := outpost.NewAPIKey(name, outpost.DefaultRequestsPerMinute)
		if err != nil {
			return err
		}
		cfg.APIKeys = append(cfg.APIKeys, key)
		if err := outpost.SaveConfig(loaded.Path, cfg); err != nil {
			return err
		}
		fmt.Println("Created API key:", key.ID)
		fmt.Println("Token, shown once:")
		fmt.Println(token)
		return nil
	case "revoke":
		if len(args) < 2 {
			return errors.New("usage: outpost keys revoke <id-or-prefix>")
		}
		if !cfg.RevokeKey(args[1]) {
			return fmt.Errorf("no active key matched %q", args[1])
		}
		return outpost.SaveConfig(loaded.Path, cfg)
	default:
		return fmt.Errorf("unknown keys command %q", args[0])
	}
}

func logs(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: outpost logs <tail|search>")
	}
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "tail":
		return outpost.TailLogs(loaded.Config.LogPath, os.Stdout)
	case "search":
		if len(args) < 2 {
			return errors.New("usage: outpost logs search <query>")
		}
		return outpost.SearchLogs(loaded.Config.LogPath, strings.Join(args[1:], " "), os.Stdout)
	default:
		return fmt.Errorf("unknown logs command %q", args[0])
	}
}

func expose(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: outpost expose <local|lan>")
	}
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "local":
		loaded.Config.ListenAddr = outpost.DefaultListenAddr
	case "lan":
		loaded.Config.ListenAddr = outpost.DefaultLANListenAddr
	default:
		return fmt.Errorf("transport %q is not implemented in this MVP", args[0])
	}
	if err := outpost.SaveConfig(loaded.Path, loaded.Config); err != nil {
		return err
	}
	fmt.Println("Listen address set to", loaded.Config.ListenAddr)
	return nil
}

func publish(args []string) error {
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	relayURL := flags.String("relay", envOrDefault("OUTPOST_RELAY_URL", "http://"+outpost.DefaultRelayListenAddr), "relay base URL")
	slug := flags.String("slug", envOrDefault("OUTPOST_RELAY_SLUG", outpost.DefaultRelaySlug), "public relay slug")
	relayToken := flags.String("relay-token", envOrDefault("OUTPOST_RELAY_TOKEN", outpost.DefaultRelayToken), "relay agent bearer token")
	publicToken := flags.String("public-token", envOrDefault("OUTPOST_RELAY_PUBLIC_TOKEN", ""), "optional relay-side public request token to show clients")
	publicAuthHeader := flags.String("public-auth-header", envOrDefault("OUTPOST_RELAY_PUBLIC_AUTH_HEADER", outpost.DefaultRelayPublicAuthHeader), "header used for relay-side public request auth")
	apiKey := flags.String("api-key", envOrDefault("OUTPOST_API_KEY", ""), "existing Outpost API key to show clients")
	target := flags.String("target", "", "local Outpost target base URL; defaults to config listen address")
	listen := flags.String("listen", "", "override local Outpost listen address before publishing")
	noStart := flags.Bool("no-start", false, "require an already-running local Outpost server")
	once := flags.Bool("once", false, "connect once without reconnecting")
	initialBackoff := flags.Duration("backoff-initial", 500*time.Millisecond, "initial reconnect backoff")
	maxBackoff := flags.Duration("backoff-max", 30*time.Second, "maximum reconnect backoff")
	if err := flags.Parse(args); err != nil {
		return err
	}

	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	if loaded.Created {
		fmt.Fprintln(os.Stderr, "Created Outpost config:", loaded.Path)
		fmt.Fprintln(os.Stderr, "Initial API key, shown once:")
		fmt.Fprintln(os.Stderr, loaded.InitialKey)
		fmt.Fprintln(os.Stderr)
	}

	cfg := loaded.Config
	dirty := false
	if *listen != "" && cfg.ListenAddr != *listen {
		cfg.ListenAddr = *listen
		dirty = true
	}

	identity, createdIdentity, err := cfg.EnsureRelayIdentity()
	if err != nil {
		return err
	}
	dirty = dirty || createdIdentity

	outpostAPIKey := *apiKey
	createdAPIKey := false
	if outpostAPIKey == "" {
		key, token, err := outpost.NewAPIKey("publish:"+outpost.NormalizeRelaySlug(*slug), outpost.DefaultRequestsPerMinute)
		if err != nil {
			return err
		}
		cfg.APIKeys = append(cfg.APIKeys, key)
		outpostAPIKey = token
		createdAPIKey = true
		dirty = true
	}

	if dirty {
		if err := outpost.SaveConfig(loaded.Path, cfg); err != nil {
			return err
		}
	}

	targetBase := strings.TrimRight(*target, "/")
	if targetBase == "" {
		targetBase = "http://" + cfg.ListenAddr
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	serverStarted, shutdown, err := ensureLocalOutpost(ctx, cfg, targetBase, *target != "", !*noStart)
	if err != nil {
		return err
	}
	if shutdown != nil {
		defer shutdown()
	}

	printPublishSummary(os.Stdout, publishSummary{
		RelayURL:         *relayURL,
		Slug:             *slug,
		Target:           targetBase,
		DeviceID:         identity.DeviceID,
		OutpostAPIKey:    outpostAPIKey,
		PublicToken:      *publicToken,
		PublicAuthHeader: *publicAuthHeader,
		ServerStarted:    serverStarted,
		CreatedAPIKey:    createdAPIKey,
	})

	err = outpost.RunRelayClient(ctx, outpost.RelayClientOptions{
		RelayURL:                *relayURL,
		Slug:                    *slug,
		Target:                  targetBase,
		Token:                   *relayToken,
		DeviceID:                identity.DeviceID,
		Reconnect:               !*once,
		InitialReconnectBackoff: *initialBackoff,
		MaxReconnectBackoff:     *maxBackoff,
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func relay(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: outpost relay <serve|connect|hosted>")
	}

	switch args[0] {
	case "endpoint":
		return relayEndpoint(args[1:])
	case "serve":
		flags := flag.NewFlagSet("relay serve", flag.ContinueOnError)
		var reservations relayReservationFlags
		listen := flags.String("listen", defaultRelayListenAddr(), "relay listen address")
		token := flags.String("token", envOrDefault("OUTPOST_RELAY_TOKEN", outpost.DefaultRelayToken), "agent bearer token")
		publicToken := flags.String("public-token", envOrDefault("OUTPOST_RELAY_PUBLIC_TOKEN", ""), "optional relay-side public request token")
		publicAuthHeader := flags.String("public-auth-header", envOrDefault("OUTPOST_RELAY_PUBLIC_AUTH_HEADER", outpost.DefaultRelayPublicAuthHeader), "header used for relay-side public request auth")
		registryPath := flags.String("registry", "", "relay endpoint registry path")
		openRegistration := flags.Bool("open-registration", envBool("OUTPOST_RELAY_OPEN_REGISTRATION", false), "allow unreserved relay slugs even when reservations are configured")
		flags.Var(&reservations, "reserve", "reserve a public slug for a device ID, formatted slug=device-id; repeatable")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		allReservations, loadedRegistryPath, err := loadRelayReservations(*registryPath)
		if err != nil {
			return err
		}
		for slug, reservation := range reservations.Map() {
			allReservations[slug] = reservation
		}
		if len(allReservations) > 0 {
			fmt.Println("Relay registry:", loadedRegistryPath)
		}
		return outpost.RunRelayServer(context.Background(), outpost.RelayServerOptions{
			ListenAddr:       *listen,
			Token:            *token,
			PublicToken:      *publicToken,
			PublicAuthHeader: *publicAuthHeader,
			Reservations:     allReservations,
			AllowUnreserved:  len(allReservations) == 0 || *openRegistration,
		})
	case "connect":
		flags := flag.NewFlagSet("relay connect", flag.ContinueOnError)
		relayURL := flags.String("relay", envOrDefault("OUTPOST_RELAY_URL", "http://"+outpost.DefaultRelayListenAddr), "relay base URL")
		slug := flags.String("slug", envOrDefault("OUTPOST_RELAY_SLUG", outpost.DefaultRelaySlug), "public relay slug")
		target := flags.String("target", outpost.DefaultRelayTarget, "local target base URL")
		token := flags.String("token", envOrDefault("OUTPOST_RELAY_TOKEN", outpost.DefaultRelayToken), "relay bearer token")
		deviceID := flags.String("device-id", "", "relay device ID; defaults to persistent local identity")
		once := flags.Bool("once", false, "connect once without reconnecting")
		initialBackoff := flags.Duration("backoff-initial", 500*time.Millisecond, "initial reconnect backoff")
		maxBackoff := flags.Duration("backoff-max", 30*time.Second, "maximum reconnect backoff")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		resolvedDeviceID := *deviceID
		if resolvedDeviceID == "" {
			identity, err := ensureRelayIdentity()
			if err != nil {
				return err
			}
			resolvedDeviceID = identity.DeviceID
		}
		return outpost.RunRelayClient(context.Background(), outpost.RelayClientOptions{
			RelayURL:                *relayURL,
			Slug:                    *slug,
			Target:                  *target,
			Token:                   *token,
			DeviceID:                resolvedDeviceID,
			Reconnect:               !*once,
			InitialReconnectBackoff: *initialBackoff,
			MaxReconnectBackoff:     *maxBackoff,
		})
	case "identity":
		identity, err := ensureRelayIdentity()
		if err != nil {
			return err
		}
		fmt.Println(identity.DeviceID)
		return nil
	case "hosted":
		return relayHosted(args[1:])
	default:
		return fmt.Errorf("unknown relay command %q", args[0])
	}
}

func relayHosted(args []string) error {
	if len(args) == 0 {
		args = []string{"prepare"}
	}
	if args[0] != "prepare" {
		return fmt.Errorf("unknown relay hosted command %q", args[0])
	}

	flags := flag.NewFlagSet("relay hosted prepare", flag.ContinueOnError)
	dir := flags.String("dir", "outpost-hosted-relay", "directory for generated hosted relay files")
	relayURL := flags.String("relay", envOrDefault("OUTPOST_RELAY_URL", "https://your-relay.example.com"), "public hosted relay URL")
	platform := flags.String("platform", "docker", "hosted target: docker or railway")
	slug := flags.String("slug", envOrDefault("OUTPOST_RELAY_SLUG", outpost.DefaultRelaySlug), "public relay slug")
	deviceID := flags.String("device", "local", "device ID to reserve, or local")
	agentToken := flags.String("agent-token", envOrDefault("OUTPOST_RELAY_TOKEN", "auto"), "hosted relay agent token: auto or explicit token")
	publicToken := flags.String("public-token", envOrDefault("OUTPOST_RELAY_PUBLIC_TOKEN", "auto"), "public relay token: auto, none, or explicit token")
	publicAuthHeader := flags.String("public-auth-header", envOrDefault("OUTPOST_RELAY_PUBLIC_AUTH_HEADER", outpost.DefaultRelayPublicAuthHeader), "header used for relay-side public request auth")
	sourceDir := flags.String("source", ".", "Outpost source directory used by docker compose")
	force := flags.Bool("force", false, "overwrite existing generated files")
	jsonOutput := flags.Bool("json", false, "print machine-readable JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}

	resolvedDeviceID := *deviceID
	if resolvedDeviceID == "" || strings.EqualFold(resolvedDeviceID, "local") {
		identity, err := ensureRelayIdentity()
		if err != nil {
			return err
		}
		resolvedDeviceID = identity.DeviceID
	}

	result, err := prepareHostedRelayBundle(hostedRelayPrepareOptions{
		Dir:              *dir,
		RelayURL:         *relayURL,
		Platform:         *platform,
		Slug:             *slug,
		DeviceID:         resolvedDeviceID,
		AgentToken:       *agentToken,
		PublicToken:      *publicToken,
		PublicAuthHeader: *publicAuthHeader,
		SourceDir:        *sourceDir,
		Force:            *force,
	})
	if err != nil {
		return err
	}

	if *jsonOutput {
		encoded, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	}
	printHostedRelayPrepareSummary(os.Stdout, result)
	return nil
}

func relayEndpoint(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: outpost relay endpoint <create|list|revoke|path>")
	}

	switch args[0] {
	case "create":
		if len(args) < 2 {
			return errors.New("usage: outpost relay endpoint create <slug> [--device local] [--public-token auto]")
		}
		flags := flag.NewFlagSet("relay endpoint create", flag.ContinueOnError)
		deviceID := flags.String("device", "local", "device ID to reserve, or local")
		publicToken := flags.String("public-token", "auto", "public relay token: auto, none, or explicit token")
		publicAuthHeader := flags.String("public-auth-header", outpost.DefaultRelayPublicAuthHeader, "header used for relay-side public request auth")
		registryPath := flags.String("registry", "", "relay endpoint registry path")
		replace := flags.Bool("replace", false, "replace an existing active endpoint")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}

		resolvedDeviceID := *deviceID
		if resolvedDeviceID == "" || strings.EqualFold(resolvedDeviceID, "local") {
			identity, err := ensureRelayIdentity()
			if err != nil {
				return err
			}
			resolvedDeviceID = identity.DeviceID
		}

		loaded, err := outpost.LoadRelayRegistry(*registryPath)
		if err != nil {
			return err
		}
		endpoint, token, err := loaded.Registry.CreateEndpoint(outpost.RelayEndpointCreateOptions{
			Slug:             args[1],
			DeviceID:         resolvedDeviceID,
			PublicToken:      *publicToken,
			PublicAuthHeader: *publicAuthHeader,
			Replace:          *replace,
		})
		if err != nil {
			return err
		}
		if err := outpost.SaveRelayRegistry(loaded.Path, loaded.Registry); err != nil {
			return err
		}

		fmt.Println("Created relay endpoint:", endpoint.Slug)
		fmt.Println("Device:", endpoint.DeviceID)
		fmt.Println("Registry:", loaded.Path)
		if token != "" {
			fmt.Println("Public relay token, shown once:")
			fmt.Println(token)
			fmt.Println("Client relay header:")
			fmt.Printf("%s: Bearer %s\n", endpoint.PublicAuthHeader, token)
		} else {
			fmt.Println("Public relay token: none")
		}
		return nil
	case "list":
		flags := flag.NewFlagSet("relay endpoint list", flag.ContinueOnError)
		registryPath := flags.String("registry", "", "relay endpoint registry path")
		all := flags.Bool("all", false, "include revoked endpoints")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		loaded, err := outpost.LoadRelayRegistry(*registryPath)
		if err != nil {
			return err
		}
		endpoints := loaded.Registry.ActiveEndpoints()
		if *all {
			endpoints = append([]outpost.RelayEndpoint(nil), loaded.Registry.Endpoints...)
			sort.Slice(endpoints, func(i, j int) bool {
				if endpoints[i].Slug == endpoints[j].Slug {
					return endpoints[i].CreatedAt.Before(endpoints[j].CreatedAt)
				}
				return endpoints[i].Slug < endpoints[j].Slug
			})
		}
		fmt.Println("Slug\tDevice\tTokenPrefix\tAuthHeader\tCreated\tRevoked")
		for _, endpoint := range endpoints {
			revoked := "-"
			if endpoint.RevokedAt != nil {
				revoked = endpoint.RevokedAt.Format(time.RFC3339)
			}
			tokenPrefix := endpoint.PublicTokenPrefix
			if tokenPrefix == "" {
				tokenPrefix = "-"
			}
			authHeader := endpoint.PublicAuthHeader
			if authHeader == "" {
				authHeader = outpost.DefaultRelayPublicAuthHeader
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
				endpoint.Slug,
				endpoint.DeviceID,
				tokenPrefix,
				authHeader,
				endpoint.CreatedAt.Format(time.RFC3339),
				revoked,
			)
		}
		return nil
	case "revoke":
		if len(args) < 2 {
			return errors.New("usage: outpost relay endpoint revoke <slug>")
		}
		flags := flag.NewFlagSet("relay endpoint revoke", flag.ContinueOnError)
		registryPath := flags.String("registry", "", "relay endpoint registry path")
		if err := flags.Parse(args[2:]); err != nil {
			return err
		}
		loaded, err := outpost.LoadRelayRegistry(*registryPath)
		if err != nil {
			return err
		}
		if !loaded.Registry.RevokeEndpoint(args[1]) {
			return fmt.Errorf("no active relay endpoint matched %q", args[1])
		}
		if err := outpost.SaveRelayRegistry(loaded.Path, loaded.Registry); err != nil {
			return err
		}
		fmt.Println("Revoked relay endpoint:", outpost.NormalizeRelaySlug(args[1]))
		return nil
	case "path":
		flags := flag.NewFlagSet("relay endpoint path", flag.ContinueOnError)
		registryPath := flags.String("registry", "", "relay endpoint registry path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		path, err := outpost.ResolveRelayRegistryPath(*registryPath)
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	default:
		return fmt.Errorf("unknown relay endpoint command %q", args[0])
	}
}

type hostedRelayPrepareOptions struct {
	Dir              string
	RelayURL         string
	Platform         string
	Slug             string
	DeviceID         string
	AgentToken       string
	PublicToken      string
	PublicAuthHeader string
	SourceDir        string
	Force            bool
}

type hostedRelayPrepareResult struct {
	BundleDir        string            `json:"bundle_dir"`
	RelayURL         string            `json:"relay_url"`
	Platform         string            `json:"platform"`
	Slug             string            `json:"slug"`
	DeviceID         string            `json:"device_id"`
	AgentToken       string            `json:"agent_token"`
	PublicToken      string            `json:"public_token,omitempty"`
	PublicAuthHeader string            `json:"public_auth_header"`
	BaseURL          string            `json:"base_url"`
	Files            map[string]string `json:"files"`
}

func prepareHostedRelayBundle(opts hostedRelayPrepareOptions) (hostedRelayPrepareResult, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		dir = "outpost-hosted-relay"
	}
	bundleDir, err := filepath.Abs(dir)
	if err != nil {
		return hostedRelayPrepareResult{}, err
	}

	relayURL := strings.TrimRight(strings.TrimSpace(opts.RelayURL), "/")
	if relayURL == "" {
		relayURL = "https://your-relay.example.com"
	}
	platform := strings.TrimSpace(strings.ToLower(opts.Platform))
	if platform == "" {
		platform = "docker"
	}
	if platform != "docker" && platform != "railway" {
		return hostedRelayPrepareResult{}, fmt.Errorf("unsupported hosted relay platform %q", opts.Platform)
	}
	slug := outpost.NormalizeRelaySlug(opts.Slug)
	if slug == "" {
		slug = outpost.DefaultRelaySlug
	}
	deviceID := outpost.NormalizeRelayDeviceID(opts.DeviceID)
	if deviceID == "" {
		return hostedRelayPrepareResult{}, errors.New("hosted relay device ID is empty")
	}
	publicAuthHeader := strings.TrimSpace(opts.PublicAuthHeader)
	if publicAuthHeader == "" {
		publicAuthHeader = outpost.DefaultRelayPublicAuthHeader
	}

	agentToken := strings.TrimSpace(opts.AgentToken)
	if agentToken == "" || strings.EqualFold(agentToken, "auto") {
		agentToken, err = outpost.NewRelayAgentToken()
		if err != nil {
			return hostedRelayPrepareResult{}, err
		}
	}

	sourceDir := strings.TrimSpace(opts.SourceDir)
	if sourceDir == "" {
		sourceDir = "."
	}
	sourceDir, err = filepath.Abs(sourceDir)
	if err != nil {
		return hostedRelayPrepareResult{}, err
	}

	registry := &outpost.RelayRegistry{}
	endpoint, publicToken, err := registry.CreateEndpoint(outpost.RelayEndpointCreateOptions{
		Slug:             slug,
		DeviceID:         deviceID,
		PublicToken:      opts.PublicToken,
		PublicAuthHeader: publicAuthHeader,
		Replace:          true,
	})
	if err != nil {
		return hostedRelayPrepareResult{}, err
	}

	registryPretty, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return hostedRelayPrepareResult{}, err
	}
	registryPretty = append(registryPretty, '\n')
	registryCompact, err := json.Marshal(registry)
	if err != nil {
		return hostedRelayPrepareResult{}, err
	}
	registryB64 := base64.StdEncoding.EncodeToString(registryCompact)

	files := map[string]string{
		"compose":  filepath.Join(bundleDir, "docker-compose.yml"),
		"env":      filepath.Join(bundleDir, "relay.env"),
		"registry": filepath.Join(bundleDir, "relay_endpoints.json"),
		"readme":   filepath.Join(bundleDir, "README.md"),
	}
	if platform == "railway" {
		files["railway"] = filepath.Join(bundleDir, "railway.json")
		files["railway_env"] = filepath.Join(bundleDir, "railway.env")
	}

	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		return hostedRelayPrepareResult{}, err
	}
	if err := writeHostedFile(files["registry"], registryPretty, 0o600, opts.Force); err != nil {
		return hostedRelayPrepareResult{}, err
	}
	if err := writeHostedFile(files["env"], []byte(hostedRelayEnvFile(agentToken, registryB64)), 0o600, opts.Force); err != nil {
		return hostedRelayPrepareResult{}, err
	}
	if err := writeHostedFile(files["compose"], []byte(hostedRelayComposeFile(sourceDir)), 0o644, opts.Force); err != nil {
		return hostedRelayPrepareResult{}, err
	}
	if platform == "railway" {
		if err := writeHostedFile(files["railway"], []byte(hostedRelayRailwayConfig()), 0o644, opts.Force); err != nil {
			return hostedRelayPrepareResult{}, err
		}
		if err := writeHostedFile(files["railway_env"], []byte(hostedRelayRailwayEnvFile(agentToken, registryB64)), 0o600, opts.Force); err != nil {
			return hostedRelayPrepareResult{}, err
		}
	}

	result := hostedRelayPrepareResult{
		BundleDir:        bundleDir,
		RelayURL:         relayURL,
		Platform:         platform,
		Slug:             endpoint.Slug,
		DeviceID:         endpoint.DeviceID,
		AgentToken:       agentToken,
		PublicToken:      publicToken,
		PublicAuthHeader: publicAuthHeader,
		BaseURL:          relayPublicBaseURL(relayURL, endpoint.Slug),
		Files:            files,
	}

	if err := writeHostedFile(files["readme"], []byte(hostedRelayReadme(result, sourceDir)), 0o644, opts.Force); err != nil {
		return hostedRelayPrepareResult{}, err
	}
	return result, nil
}

func hostedRelayEnvFile(agentToken string, registryB64 string) string {
	return strings.Join([]string{
		"OUTPOST_RELAY_LISTEN=0.0.0.0:8787",
		"OUTPOST_RELAY_REGISTRY=/data/relay_endpoints.json",
		"OUTPOST_RELAY_TOKEN=" + agentToken,
		"OUTPOST_RELAY_ENDPOINTS_B64=" + registryB64,
		"",
	}, "\n")
}

func hostedRelayRailwayEnvFile(agentToken string, registryB64 string) string {
	return strings.Join([]string{
		"OUTPOST_RELAY_TOKEN=" + agentToken,
		"OUTPOST_RELAY_ENDPOINTS_B64=" + registryB64,
		"RAILWAY_DOCKERFILE_PATH=Dockerfile.relay",
		"RAILWAY_HEALTHCHECK_TIMEOUT_SEC=300",
		"",
	}, "\n")
}

func hostedRelayRailwayConfig() string {
	return `{
  "$schema": "https://railway.com/railway.schema.json",
  "build": {
    "builder": "DOCKERFILE",
    "dockerfilePath": "Dockerfile.relay"
  },
  "deploy": {
    "healthcheckPath": "/healthz",
    "healthcheckTimeout": 300,
    "restartPolicyType": "ON_FAILURE",
    "restartPolicyMaxRetries": 10
  }
}
`
}

func hostedRelayComposeFile(sourceDir string) string {
	source, err := json.Marshal(sourceDir)
	if err != nil {
		source = []byte(`"."`)
	}
	return fmt.Sprintf(`services:
  relay:
    build:
      context: %s
      dockerfile: Dockerfile.relay
    env_file:
      - relay.env
    volumes:
      - ./relay_endpoints.json:/data/relay_endpoints.json:ro
    ports:
      - "8787:8787"
    restart: unless-stopped
`, source)
}

func hostedRelayReadme(result hostedRelayPrepareResult, sourceDir string) string {
	lines := []string{
		"# Outpost Hosted Relay",
		"",
		"Outpost desktop settings:",
		"",
		"```text",
		"Relay profile: Hosted relay",
		"Relay URL: " + result.RelayURL,
		"Publish slug: " + result.Slug,
		"Agent token: " + result.AgentToken,
		"```",
		"",
		"Client base URL:",
		"",
		"```text",
		result.BaseURL,
		"```",
	}
	if result.Platform == "railway" {
		lines = append(lines,
			"",
			"Railway deploy:",
			"",
			"1. Create a Railway service from the Outpost GitHub repo.",
			"2. Railway will use `railway.json` and `Dockerfile.relay` from the repo root.",
			"3. Paste the variables from `railway.env` into the service Variables tab.",
			"4. After Railway gives you a public domain, set Relay URL in the desktop app to that HTTPS URL.",
		)
	} else {
		lines = append(lines,
			"",
			"Start the relay:",
			"",
			"```sh",
			"docker compose up --build",
			"```",
		)
	}
	if result.PublicToken != "" {
		lines = append(lines,
			"",
			"Client relay header:",
			"",
			"```text",
			result.PublicAuthHeader+": Bearer "+result.PublicToken,
			"```",
		)
	}
	lines = append(lines,
		"",
		"Files:",
		"",
		"```text",
		"Source: "+sourceDir,
		"Registry: "+result.Files["registry"],
		"Environment: "+result.Files["env"],
		"```",
		"",
	)
	return strings.Join(lines, "\n")
}

func printHostedRelayPrepareSummary(w io.Writer, result hostedRelayPrepareResult) {
	fmt.Fprintln(w, "Prepared hosted relay bundle:", result.BundleDir)
	fmt.Fprintln(w, "Platform:", result.Platform)
	fmt.Fprintln(w, "Relay URL:", result.RelayURL)
	fmt.Fprintln(w, "OpenAI base URL:", result.BaseURL)
	fmt.Fprintln(w, "Relay device:", result.DeviceID)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Desktop hosted settings:")
	fmt.Fprintln(w, "  Relay profile: Hosted relay")
	fmt.Fprintln(w, "  Publish slug:", result.Slug)
	fmt.Fprintln(w, "  Agent token:", result.AgentToken)
	if result.PublicToken != "" {
		fmt.Fprintf(w, "  Relay header: %s: Bearer %s\n", result.PublicAuthHeader, result.PublicToken)
	}
	fmt.Fprintln(w)
	if result.Platform == "railway" {
		fmt.Fprintln(w, "Railway variables:")
		fmt.Fprintln(w, "  Paste", result.Files["railway_env"], "into the Railway service Variables tab.")
		fmt.Fprintln(w, "  After deploy, copy the Railway public domain into the desktop Relay URL.")
	} else {
		fmt.Fprintln(w, "Start the hosted relay with:")
		fmt.Fprintln(w, "  cd", result.BundleDir)
		fmt.Fprintln(w, "  docker compose up --build")
	}
}

func writeHostedFile(path string, data []byte, perm os.FileMode, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, perm)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%s already exists; use --force to overwrite it", path)
		}
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Chmod(perm)
}

func loadRelayReservations(path string) (map[string]outpost.RelayReservation, string, error) {
	loaded, err := outpost.LoadRelayRegistry(path)
	if err != nil {
		return nil, "", err
	}
	reservations := loaded.Registry.Reservations()
	sources := []string{loaded.Path}
	if err := mergeRelayReservationsFromEnv(reservations, &sources); err != nil {
		return nil, "", err
	}
	return reservations, strings.Join(sources, ", "), nil
}

func mergeRelayReservationsFromEnv(reservations map[string]outpost.RelayReservation, sources *[]string) error {
	if encoded := strings.TrimSpace(os.Getenv("OUTPOST_RELAY_ENDPOINTS_B64")); encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return fmt.Errorf("decode OUTPOST_RELAY_ENDPOINTS_B64: %w", err)
		}
		if err := mergeRelayRegistryJSON(reservations, data); err != nil {
			return fmt.Errorf("parse OUTPOST_RELAY_ENDPOINTS_B64: %w", err)
		}
		*sources = append(*sources, "OUTPOST_RELAY_ENDPOINTS_B64")
	}
	if raw := strings.TrimSpace(os.Getenv("OUTPOST_RELAY_ENDPOINTS_JSON")); raw != "" {
		if err := mergeRelayRegistryJSON(reservations, []byte(raw)); err != nil {
			return fmt.Errorf("parse OUTPOST_RELAY_ENDPOINTS_JSON: %w", err)
		}
		*sources = append(*sources, "OUTPOST_RELAY_ENDPOINTS_JSON")
	}
	return nil
}

func mergeRelayRegistryJSON(reservations map[string]outpost.RelayReservation, data []byte) error {
	var registry outpost.RelayRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return err
	}
	for slug, reservation := range registry.Reservations() {
		reservations[slug] = reservation
	}
	return nil
}

func ensureRelayIdentity() (outpost.RelayIdentity, error) {
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return outpost.RelayIdentity{}, err
	}
	if loaded.Created {
		fmt.Fprintln(os.Stderr, "Created Outpost config:", loaded.Path)
		fmt.Fprintln(os.Stderr, "Initial API key, shown once:")
		fmt.Fprintln(os.Stderr, loaded.InitialKey)
		fmt.Fprintln(os.Stderr)
	}
	identity, created, err := loaded.Config.EnsureRelayIdentity()
	if err != nil {
		return outpost.RelayIdentity{}, err
	}
	if created {
		if err := outpost.SaveConfig(loaded.Path, loaded.Config); err != nil {
			return outpost.RelayIdentity{}, err
		}
	}
	return identity, nil
}

func newOutpostHTTPServer(cfg *outpost.Config) *http.Server {
	logger := outpost.NewRequestLogger(cfg.LogPath)
	server := outpost.NewServer(cfg, logger)
	return &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func ensureLocalOutpost(ctx context.Context, cfg *outpost.Config, targetBase string, customTarget bool, canStart bool) (bool, func(), error) {
	if outpostHealthOK(ctx, targetBase) {
		return false, nil, nil
	}
	if customTarget {
		return false, nil, fmt.Errorf("custom target %s is not reachable; start it first or omit --target", targetBase)
	}
	if !canStart {
		return false, nil, fmt.Errorf("local Outpost is not running at %s", targetBase)
	}

	httpServer := newOutpostHTTPServer(cfg)
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.ListenAndServe()
	}()

	readyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return false, nil, err
			}
			return false, nil, err
		case <-readyCtx.Done():
			_ = httpServer.Shutdown(context.Background())
			if ctx.Err() != nil {
				return false, nil, ctx.Err()
			}
			return false, nil, fmt.Errorf("timed out waiting for local Outpost at %s", targetBase)
		case <-ticker.C:
			if outpostHealthOK(ctx, targetBase) {
				shutdown := func() {
					shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					_ = httpServer.Shutdown(shutdownCtx)
					select {
					case <-errCh:
					case <-time.After(500 * time.Millisecond):
					}
				}
				return true, shutdown, nil
			}
		}
	}
}

func outpostHealthOK(ctx context.Context, targetBase string) bool {
	healthURL, err := resolveTargetPath(targetBase, "/healthz")
	if err != nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(checkCtx, http.MethodGet, healthURL, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func resolveTargetPath(base string, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	return parsed.ResolveReference(&url.URL{Path: path}).String(), nil
}

type publishSummary struct {
	RelayURL         string
	Slug             string
	Target           string
	DeviceID         string
	OutpostAPIKey    string
	PublicToken      string
	PublicAuthHeader string
	ServerStarted    bool
	CreatedAPIKey    bool
}

func printPublishSummary(w io.Writer, summary publishSummary) {
	publicBase := relayPublicBaseURL(summary.RelayURL, summary.Slug)
	fmt.Fprintln(w, "Outpost publish is running")
	fmt.Fprintln(w, "OpenAI base URL:", publicBase)
	fmt.Fprintln(w, "Local target:", summary.Target)
	fmt.Fprintln(w, "Relay device:", summary.DeviceID)
	if summary.ServerStarted {
		fmt.Fprintln(w, "Local server: started by publish")
	} else {
		fmt.Fprintln(w, "Local server: already running")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Client headers:")
	if summary.PublicToken != "" {
		fmt.Fprintf(w, "  %s: Bearer %s\n", summary.PublicAuthHeader, summary.PublicToken)
	}
	fmt.Fprintf(w, "  Authorization: Bearer %s\n", summary.OutpostAPIKey)
	if summary.CreatedAPIKey {
		fmt.Fprintln(w, "  # This Outpost API key was created for this publish session and is shown once.")
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Press Ctrl-C to stop publishing.")
}

func relayPublicBaseURL(relayURL string, slug string) string {
	base := strings.TrimRight(relayURL, "/")
	return base + "/" + outpost.NormalizeRelaySlug(slug) + "/v1"
}

func config(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: outpost config <path|print|edit>")
	}
	loaded, err := outpost.LoadOrCreateConfig("")
	if err != nil {
		return err
	}
	switch args[0] {
	case "path":
		fmt.Println(loaded.Path)
		return nil
	case "print":
		encoded, err := json.MarshalIndent(loaded.Config, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(encoded))
		return nil
	case "edit":
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		cmd := exec.Command(editor, loaded.Path)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	default:
		return fmt.Errorf("unknown config command %q", args[0])
	}
}

func usage() {
	fmt.Println(`Usage:
  outpost start
  outpost status
  outpost keys list
  outpost keys create <name>
  outpost keys revoke <id-or-prefix>
  outpost logs tail
  outpost logs search <query>
  outpost expose local
  outpost expose lan
  outpost publish [--slug demo] [--relay http://127.0.0.1:8787]
  outpost relay identity
  outpost relay endpoint create <slug> [--device local] [--public-token auto]
  outpost relay endpoint list
  outpost relay endpoint revoke <slug>
  outpost relay hosted prepare [--relay https://relay.example.com] [--slug demo]
  outpost relay serve [--listen 127.0.0.1:8787] [--token dev] [--reserve demo=device-id] [--public-token token]
  outpost relay connect [--relay http://127.0.0.1:8787] [--slug demo] [--target http://127.0.0.1:7341] [--token dev]
  outpost config path
  outpost config print
  outpost config edit`)
}

func envOrDefault(name string, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func defaultRelayListenAddr() string {
	if listen := strings.TrimSpace(os.Getenv("OUTPOST_RELAY_LISTEN")); listen != "" {
		return listen
	}
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return "0.0.0.0:" + port
	}
	return outpost.DefaultRelayListenAddr
}

func envBool(name string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	switch strings.ToLower(value) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

type relayReservationFlags map[string]outpost.RelayReservation

func (f *relayReservationFlags) String() string {
	if f == nil || len(*f) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*f))
	for slug, reservation := range *f {
		parts = append(parts, slug+"="+reservation.DeviceID)
	}
	return strings.Join(parts, ",")
}

func (f *relayReservationFlags) Set(value string) error {
	slug, deviceID, ok := strings.Cut(value, "=")
	if !ok {
		return fmt.Errorf("reservation must be formatted slug=device-id")
	}
	slug = outpost.NormalizeRelaySlug(slug)
	deviceID = outpost.NormalizeRelayDeviceID(deviceID)
	if slug == "" {
		return fmt.Errorf("reservation slug is empty")
	}
	if deviceID == "" {
		return fmt.Errorf("reservation device ID is empty")
	}
	if *f == nil {
		*f = relayReservationFlags{}
	}
	(*f)[slug] = outpost.RelayReservation{Slug: slug, DeviceID: deviceID}
	return nil
}

func (f relayReservationFlags) Map() map[string]outpost.RelayReservation {
	reservations := map[string]outpost.RelayReservation{}
	for slug, reservation := range f {
		reservations[slug] = reservation
	}
	return reservations
}
