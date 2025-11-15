package config

import (
	"testing"

	"github.com/cloudflare/circl/hpke"
	"github.com/confidentsecurity/ohttp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalUnmarshalKeyConfig_RoundTrip(t *testing.T) {
	t.Helper()
	kemID := hpke.KEM_X25519_HKDF_SHA256
	scheme := kemID.Scheme()
	pk, _, err2 := scheme.GenerateKeyPair()
	require.NoError(t, err2, "generate test keypair")
	kc := ohttp.KeyConfig{
		KeyID:     7,
		KemID:     kemID,
		PublicKey: pk,
		SymmetricAlgorithms: []ohttp.SymmetricAlgorithm{
			{KDFID: hpke.KDF_HKDF_SHA256, AEADID: hpke.AEAD_AES128GCM},
			{KDFID: hpke.KDF_HKDF_SHA512, AEADID: hpke.AEAD_ChaCha20Poly1305},
		},
	}

	data, err := MarshalKeyConfig(kc)
	require.NoError(t, err)

	got, err := UnmarshalKeyConfig(data)
	require.NoError(t, err)
	assert.Equal(t, kc, *got)
}
