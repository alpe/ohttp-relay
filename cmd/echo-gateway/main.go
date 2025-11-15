package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"maps"

	"github.com/alpe/ohttprelay/internal/ctrl"
	"github.com/alpe/ohttprelay/pkg/config"
	"github.com/cloudflare/circl/hpke"
	"github.com/cloudflare/circl/kem"
	"github.com/confidentsecurity/ohttp"
)

const (
	defaultKeyConfigPath  = "keyconfig-ohttp.yaml"
	defaultPrivateKeyPath = "ohttp-gateway.key"
)

func main() {
	// Very small CLI: `echo-gateway gen-key` generates and persists a key pair.
	if len(os.Args) > 1 && os.Args[1] == "gen-key" {
		genKeyCmd()
		return
	}

	// Normal mode: start gateway, loading keys from disk by default.
	keyConfigPath := envOr("KEY_CONFIG_FILE", defaultKeyConfigPath)
	privKeyPath := envOr("PRIVATE_KEY_FILE", defaultPrivateKeyPath)

	keyPair, err := loadKeyPairFromDisk(keyConfigPath, privKeyPath)
	if err != nil {
		log.Fatalf("load OHTTP key pair: %v\nHint: run '%s gen-key' to generate keys (env: KEY_CONFIG_FILE, PRIVATE_KEY_FILE).", err, filepath.Base(os.Args[0]))
	}

	// Create the gateway and wrap the handler in its middleware.
	gateway, err := ohttp.NewGateway(keyPair)
	if err != nil {
		log.Fatalf("create gateway: %v", err)
	}
	// Run the server.
	ctx := ctrl.SetupSignalHandler()

	port := envOr("GATEWAY_PORT", "8888")
	log.Printf("running gateway on :%s (config=%s, priv=%s)...", port, keyConfigPath, privKeyPath)

	mux := http.NewServeMux()
	// The main OHTTP handler
	h := captureOuterHeadersMiddleware(ohttp.Middleware(gateway, http.HandlerFunc(handler)))
	mux.Handle("/", h)

	// The key configuration endpoint
	mux.HandleFunc("/.well-known/ohttp-configs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ohttp-keys")
		configBytes, err := keyPair.KeyConfig.MarshalBinary()
		if err != nil {
			http.Error(w, "failed to marshal key config", http.StatusInternalServerError)
			return
		}
		buf := make([]byte, 2+len(configBytes))
		binary.BigEndian.PutUint16(buf[:2], uint16(len(configBytes)))
		copy(buf[2:], configBytes)

		if _, err := w.Write(buf); err != nil {
			log.Printf("failed to write key config response: %v", err)
		}
	})

	// nosemgrep: go.lang.security.audit.net.use-tls.use-tls
	srv := &http.Server{Addr: ":" + port, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("run relay: %v", err)
		}
	}()
	<-ctx.Done()
	log.Println("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server shutdown failed: %v", err)
	}
	log.Println("server gracefully stopped")

}

func genKeyCmd() {
	fs := flag.NewFlagSet("gen-key", flag.ExitOnError)
	var (
		keyConfigPath string
		privKeyPath   string
	)
	fs.StringVar(&keyConfigPath, "keyconfig", envOr("KEY_CONFIG_FILE", defaultKeyConfigPath), "path to write the KeyConfig binary file")
	fs.StringVar(&privKeyPath, "priv", envOr("PRIVATE_KEY_FILE", defaultPrivateKeyPath), "path to write the private key for the gateway")
	_ = fs.Parse(os.Args[2:])

	kemID := hpke.KEM_P256_HKDF_SHA256
	pubKey, privKey, err := kemID.Scheme().GenerateKeyPair()
	if err != nil {
		log.Fatalf("generate keypair: %v", err)
	}

	kc := ohttp.KeyConfig{
		KeyID:     1,
		KemID:     kemID,
		PublicKey: pubKey,
		SymmetricAlgorithms: []ohttp.SymmetricAlgorithm{{
			KDFID:  hpke.KDF_HKDF_SHA256,
			AEADID: hpke.AEAD_AES128GCM,
		}},
	}

	if err := writeKeyConfigFile(keyConfigPath, kc); err != nil {
		log.Fatalf("write key config file: %v", err)
	}
	if err := writePrivateKeyFile(privKeyPath, privKey); err != nil {
		log.Fatalf("write private key file: %v", err)
	}
	log.Printf("generated OHTTP keys. keyconfig=%s, private=%s", keyConfigPath, privKeyPath)
}

// the request passed is the inner request decoded by ohttp
func handler(w http.ResponseWriter, r *http.Request) {
	log.Printf("%s request for %s\n", r.Method, r.URL.Path)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Retrieve captured outer headers (pre-OHTTP) from context, if available
	outerHeaders := outerHeadersFromContext(r.Context())

	response := map[string]any{
		"method":        r.Method,
		"path":          r.URL.Path,
		"headers":       r.Header,
		"outer-headers": outerHeaders,
		"body":          string(body),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	log.Printf("received: %#v\n", response)
}

// ctx key type to avoid collisions
type ctxKeyOuterHeaders struct{}

var outerHeadersCtxKey ctxKeyOuterHeaders

// captureOuterHeadersMiddleware captures all headers from the outer HTTP request (before
// OHTTP decoding) and attaches them to the request context so that the inner handler can
// include them in the response payload.
func captureOuterHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		ctx := context.WithValue(r.Context(), outerHeadersCtxKey, r.Header.Clone())
		// call the next handler in the chain
		next.ServeHTTP(rec, r.WithContext(ctx))

		header := rec.Header()
		maps.Copy(header, header)
		header.Set("X-Test-Gateway-Outer", "any value")
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
}

func outerHeadersFromContext(ctx context.Context) http.Header {
	if ctx == nil {
		return nil
	}
	if v := ctx.Value(outerHeadersCtxKey); v != nil {
		if h, ok := v.(http.Header); ok {
			return h
		}
	}
	return nil
}

func writeKeyConfigFile(name string, kc ohttp.KeyConfig) error {
	data, err := config.MarshalKeyConfig(kc)
	if err != nil {
		return fmt.Errorf("marshal keyconfig to binary: %w", err)
	}
	if err := os.WriteFile(name, data, 0o644); err != nil {
		return fmt.Errorf("write keyconfig file: %w", err)
	}
	return nil
}

func writePrivateKeyFile(name string, sk kem.PrivateKey) error {
	b, err := sk.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	if err := os.WriteFile(name, b, 0o600); err != nil {
		return fmt.Errorf("write private key file: %w", err)
	}
	return nil
}

func loadKeyPairFromDisk(keyConfigPath, privKeyPath string) (ohttp.KeyPair, error) {
	var kp ohttp.KeyPair
	kc, err := readKeyConfigFile(keyConfigPath)
	if err != nil {
		return kp, err
	}
	if kc.KemID == 0 {
		return kp, errors.New("invalid keyconfig: missing KEM id")
	}

	sk, err := readPrivateKeyFile(privKeyPath, kc.KemID)
	if err != nil {
		return kp, err
	}
	kp = ohttp.KeyPair{SecretKey: sk, KeyConfig: *kc}
	return kp, nil
}

func readKeyConfigFile(name string) (*ohttp.KeyConfig, error) {
	var kc *ohttp.KeyConfig
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read keyconfig file: %w", err)
	}
	if kc, err = config.UnmarshalKeyConfig(b); err != nil {
		return nil, fmt.Errorf("parse keyconfig: %w", err)
	}
	return kc, nil
}

func readPrivateKeyFile(name string, k hpke.KEM) (kem.PrivateKey, error) {
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read private key file: %w", err)
	}
	scheme := k.Scheme()
	sk, err := scheme.UnmarshalBinaryPrivateKey(b)
	if err != nil {
		return nil, fmt.Errorf("unmarshal private key: %w", err)
	}
	return sk, nil
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}
