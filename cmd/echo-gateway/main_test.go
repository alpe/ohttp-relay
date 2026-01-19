package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudflare/circl/hpke"
	"github.com/confidentsecurity/ohttp"
)

func TestKeyConfigEndpoint(t *testing.T) {
	// Generate a temporary key pair for testing
	kemID := hpke.KEM_P256_HKDF_SHA256
	pubKey, privKey, err := kemID.Scheme().GenerateKeyPair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
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
	keyPair := ohttp.KeyPair{SecretKey: privKey, KeyConfig: kc}

	// Setup the handler as in main.go
	gateway, err := ohttp.NewGateway(keyPair)
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}

	mux := http.NewServeMux()
	h := captureOuterHeadersMiddleware(ohttp.Middleware(gateway, http.HandlerFunc(echoHandler)))
	mux.Handle("/", h)

	mux.HandleFunc("/.wellknown/ohttp-configs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ohttp-keys")
		configBytes, err := keyPair.KeyConfig.MarshalBinary()
		if err != nil {
			http.Error(w, "failed to marshal key config", http.StatusInternalServerError)
			return
		}
		if _, err := w.Write(configBytes); err != nil {
			t.Logf("failed to write key config response: %v", err)
		}
	})

	// Test the endpoint
	req := httptest.NewRequest("GET", "/.wellknown/ohttp-configs", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/ohttp-keys" {
		t.Errorf("expected Content-Type application/ohttp-keys, got %s", contentType)
	}

	// Verify body is not empty
	if w.Body.Len() == 0 {
		t.Error("expected non-empty body")
	}
}
