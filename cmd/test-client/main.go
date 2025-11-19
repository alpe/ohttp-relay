package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/alpe/ohttp-relay/pkg/config"
	"github.com/confidentsecurity/ohttp"
)

const (
	defaultKeyConfigPath = "keyconfig-ohttp.yaml"
	defaultRelayURL      = "http://127.0.0.1:7777"
	defaultGatewayURL    = "http://127.0.0.1:8888"
)

const (
	headerKeyOuter = "X-Outer-Test-Header"
	headerKeyInner = "X-Test-Header"
)

func main() {
	// Flags with env overrides (precedence: flag > env > default).
	var (
		keyConfigPath string
		relayURL      string
		gatewayURL    string
	)
	flag.StringVar(&keyConfigPath, "keyconfig", envOr("KEY_CONFIG_FILE", ""), "path to the KeyConfig YAML file (env: KEY_CONFIG_FILE)")
	flag.StringVar(&relayURL, "relay", envOr("RELAY_URL", defaultRelayURL), "DestinationURL of the OHTTP relay (env: RELAY_URL)")
	flag.StringVar(&gatewayURL, "gateway", envOr("GATEWAY_URL", ""), "Base URL of the OHTTP gateway to fetch key config (env: GATEWAY_URL). When empty, uses default "+defaultGatewayURL)
	flag.Parse()

	if keyConfigPath != "" && gatewayURL != "" {
		log.Fatalf("both keyconfig and gateway configured")
	}

	var keyConfig ohttp.KeyConfig
	var err error
	if keyConfigPath != "" {
		log.Println("loading key config from file: " + keyConfigPath)
		if keyConfig, err = readKeyConfigFile(keyConfigPath); err != nil {
			log.Fatalf("read key config: %v", err)
		}
	} else {
		gw := gatewayURL
		if gw == "" {
			gw = defaultGatewayURL
		}
		log.Println("loading key config from server: " + gw)
		if keyConfig, err = fetchKeyConfig(gw); err != nil {
			log.Fatalf("fetch key config: %v", err)
		}
	}

	ohttpTransport, err := ohttp.NewTransport(keyConfig, relayURL)
	if err != nil {
		log.Fatalf("failed to create ohttp transport: %v", err)
	}
	transport := &outerHeaderTransport{ohttpCodec: ohttpTransport, httpClient: &http.Client{Timeout: 9 * time.Second}}

	client := &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}

	// construct a request.
	testData := map[string]string{"message": "Hello OHTTP!"}
	jsonData, err := json.Marshal(testData)
	if err != nil {
		log.Fatalf("failed to marshal test data: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, "http://ohttp.invalid", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	const innerHeaderPayload = "ping"
	req.Header.Set(headerKeyInner, innerHeaderPayload)

	// Do the request and print status code and body to stdout.
	fmt.Printf("requesting %s via OHTTP (config=%s relay=%s)\n", req.URL, keyConfigPath, relayURL)
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("❌request failed: %v", err)
	}
	defer func() {
		err = resp.Body.Close()
		if err != nil {
			log.Fatalf("❌failed to close body: %v", err)
		}
	}()

	fmt.Printf("status code %d\n", resp.StatusCode)
	var rspPayload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rspPayload); err != nil {
		log.Fatalf("failed to decode response: %v", err)
	}
	fmt.Printf("Response payload: %#v\n", rspPayload)
	fmt.Printf("Response headers: %#v\n", resp.Header)
	var failure bool
	if h, ok := rspPayload["headers"].(map[string]any)[headerKeyInner].([]any); !ok || len(h) == 0 || h[0] != any(innerHeaderPayload) {
		fmt.Println("❌Test header should be returned!")
		failure = true
	}
	if _, exists := rspPayload["outer-headers"].(map[string]any)[headerKeyOuter]; exists {
		fmt.Println("❌Test outer header should not be returned!")
		failure = true
	}
	if _, exists := resp.Header["X-Test-Gateway-Outer"]; exists {
		fmt.Println("❌Test outer response header should not be set!")
		failure = true
	}
	if failure {
		os.Exit(1)
	}
	fmt.Println("✅ Full round trip - passed!")
}

func readKeyConfigFile(name string) (ohttp.KeyConfig, error) {
	data, err := os.ReadFile(name)
	if err != nil {
		return ohttp.KeyConfig{}, fmt.Errorf("failed to read file: %w", err)
	}

	kc, err := config.UnmarshalKeyConfig(data)
	if err != nil {
		return ohttp.KeyConfig{}, fmt.Errorf("failed to unmarshal binary: %w", err)
	}
	return *kc, nil
}

// fetchKeyConfig retrieves the OHTTP key configuration from the gateway well-known endpoint.
// Per OHTTP spec, the endpoint returns Content-Type "application/ohttp-keys" and a binary payload
// produced by KeyConfig.MarshalBinary().
func fetchKeyConfig(base string) (ohttp.KeyConfig, error) {
	url := base
	if len(url) == 0 {
		return ohttp.KeyConfig{}, fmt.Errorf("empty base url")
	}
	if url[len(url)-1] == '/' {
		url = url[:len(url)-1]
	}
	url += "/.well-known/ohttp-configs"

	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Get(url)
	if err != nil {
		return ohttp.KeyConfig{}, fmt.Errorf("get %s: %w", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusOK {
		return ohttp.KeyConfig{}, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	// Content-Type check is best-effort; proceed even if it differs.
	// Some proxies may strip or alter headers; we don't fail hard here.
	// Expected is: application/ohttp-keys
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return ohttp.KeyConfig{}, fmt.Errorf("read body: %w", err)
	}

	// The OHTTP discovery endpoint returns a length-prefixed list of KeyConfigs.
	// We must strip the 2-byte length prefix before parsing the first KeyConfig.
	if len(b) < 2 {
		return ohttp.KeyConfig{}, fmt.Errorf("response too short")
	}
	// Parse length of the list (big-endian uint16)
	listLen := int(b[0])<<8 | int(b[1])
	if len(b) < 2+listLen {
		return ohttp.KeyConfig{}, fmt.Errorf("response body shorter than declared list length")
	}

	// For simplicity, we decode the first KeyConfig in the list.
	// The list data starts at b[2:]. UnmarshalBinary typically expects just the
	// bytes for one config, but implementations vary. Assuming it reads what it needs
	// from the beginning of the slice:
	var kc ohttp.KeyConfig
	if err := kc.UnmarshalBinary(b[2:]); err != nil {
		return ohttp.KeyConfig{}, fmt.Errorf("parse key config: %w", err)
	}
	return kc, nil
}

type outerHeaderTransport struct {
	ohttpCodec *ohttp.Transport
	httpClient *http.Client
}

// RoundTrip performs an HTTP request with encapsulation and decapsulation using the configured OHTTP codec.
// It sets a custom header on the encapsulated request for testing purpose only.
// Returns the HTTP response or an error if either step fails.
func (t *outerHeaderTransport) RoundTrip(req *http.Request) (resp *http.Response, err error) {
	defer func() {
		if req.Body != nil {
			_ = req.Body.Close()
		}
	}()

	encapsulated, rd, err := t.ohttpCodec.Encapsulate(req)
	if err != nil {
		return nil, fmt.Errorf("outer: encapsulate request: %w", err)
	}

	// set a custom header not within the encrypted ohttp request
	encapsulated.Header.Set(headerKeyOuter, "custom-value")
	resp, err = t.httpClient.Do(encapsulated)
	if err != nil {
		return nil, fmt.Errorf("outer: request: %w", err)
	}

	resp, err = rd.Decapsulate(req.Context(), resp)
	if err != nil {
		return nil, fmt.Errorf("outer: decapsulate response: %w", err)
	}

	return resp, nil
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}
