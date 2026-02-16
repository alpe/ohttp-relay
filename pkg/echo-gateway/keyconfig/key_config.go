package keyconfig

import (
	"encoding/hex"
	"fmt"

	"github.com/cloudflare/circl/hpke"
	"github.com/openpcc/ohttp"
	"gopkg.in/yaml.v3"
)

var nameToAEADID = map[string]hpke.AEAD{
	"AES128GCM":        hpke.AEAD_AES128GCM,
	"AES256GCM":        hpke.AEAD_AES256GCM,
	"ChaCha20Poly1305": hpke.AEAD_ChaCha20Poly1305,
}
var nameToKDFID = map[string]hpke.KDF{
	"SHA256": hpke.KDF_HKDF_SHA256,
	"SHA384": hpke.KDF_HKDF_SHA384,
	"SHA512": hpke.KDF_HKDF_SHA512,
}

var nameToKemID = map[string]hpke.KEM{
	"P256_HKDF_SHA256":        hpke.KEM_P256_HKDF_SHA256,
	"P384_HKDF_SHA384":        hpke.KEM_P384_HKDF_SHA384,
	"P521_HKDF_SHA512":        hpke.KEM_P521_HKDF_SHA512,
	"X25519_HKDF_SHA256":      hpke.KEM_X25519_HKDF_SHA256,
	"X448_HKDF_SHA512":        hpke.KEM_X448_HKDF_SHA512,
	"X25519_KYBER768_DRAFT00": hpke.KEM_X25519_KYBER768_DRAFT00,
	"KEM_XWING":               hpke.KEM_XWING,
}

func idToStr[T comparable](s T, m map[string]T) string {
	for k, v := range m {
		if v == s {
			return k
		}
	}
	return ""
}

// yamlKeyConfig is a YAML-serializable representation of ohttp.KeyConfig.
// Note: public key is stored as hex bytes
type yamlKeyConfig struct {
	KeyID               uint8  `yaml:"key_id"`
	Kem                 string `yaml:"kem"`
	PublicKey           string `yaml:"public_key"`
	SymmetricAlgorithms []struct {
		KDFID  string `yaml:"kdf"`
		AEADID string `yaml:"aead"`
	} `yaml:"symmetric_algorithms"`
}

func MarshalKeyConfig(kc ohttp.KeyConfig) ([]byte, error) {
	pK, err := kc.PublicKey.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	y := yamlKeyConfig{
		KeyID:     kc.KeyID,
		Kem:       idToStr(kc.KemID, nameToKemID),
		PublicKey: hex.EncodeToString(pK),
	}
	for _, sa := range kc.SymmetricAlgorithms {
		y.SymmetricAlgorithms = append(y.SymmetricAlgorithms, struct {
			KDFID  string `yaml:"kdf"`
			AEADID string `yaml:"aead"`
		}{KDFID: idToStr(sa.KDFID, nameToKDFID),
			AEADID: idToStr(sa.AEADID, nameToAEADID),
		})
	}
	return yaml.Marshal(&y)
}

func UnmarshalKeyConfig(data []byte) (*ohttp.KeyConfig, error) {
	var y yamlKeyConfig
	if err := yaml.Unmarshal(data, &y); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	pK, err := hex.DecodeString(y.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("public key: %w", err)
	}
	if len(pK) == 0 {
		return nil, fmt.Errorf("empty public key")
	}
	kc := ohttp.KeyConfig{KeyID: y.KeyID, KemID: nameToKemID[y.Kem]}
	kc.PublicKey, err = kc.KemID.Scheme().UnmarshalBinaryPublicKey(pK)
	if err != nil {
		return nil, fmt.Errorf("unmarshal binary public key: %w", err)
	}
	kc.SymmetricAlgorithms = make([]ohttp.SymmetricAlgorithm, len(y.SymmetricAlgorithms))
	for i, sa := range y.SymmetricAlgorithms {
		var ok bool
		if kc.SymmetricAlgorithms[i].KDFID, ok = nameToKDFID[sa.KDFID]; !ok {
			return nil, fmt.Errorf("invalid KDF in item %s", sa.KDFID)
		}
		if kc.SymmetricAlgorithms[i].AEADID, ok = nameToAEADID[sa.AEADID]; !ok {
			return nil, fmt.Errorf("invalid AEAD in item %s", sa.AEADID)
		}
	}
	return &kc, nil
}
