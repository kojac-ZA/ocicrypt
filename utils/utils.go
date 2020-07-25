/*
   Copyright The ocicrypt Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package utils

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	p11 "github.com/miekg/pkcs11"
	"golang.org/x/crypto/ssh/terminal"
	"strings"
	"syscall"

	"github.com/pkg/errors"
	"golang.org/x/crypto/openpgp"
	json "gopkg.in/square/go-jose.v2"
)

// parseJWKPrivateKey parses the input byte array as a JWK and makes sure it's a private key
func parseJWKPrivateKey(privKey []byte, prefix string) (interface{}, error) {
	jwk := json.JSONWebKey{}
	err := jwk.UnmarshalJSON(privKey)
	if err != nil {
		return nil, errors.Wrapf(err, "%s: Could not parse input as JWK", prefix)
	}
	if jwk.IsPublic() {
		return nil, fmt.Errorf("%s: JWK is not a private key", prefix)
	}
	return &jwk, nil
}

// parseJWKPublicKey parses the input byte array as a JWK
func parseJWKPublicKey(privKey []byte, prefix string) (interface{}, error) {
	jwk := json.JSONWebKey{}
	err := jwk.UnmarshalJSON(privKey)
	if err != nil {
		return nil, errors.Wrapf(err, "%s: Could not parse input as JWK", prefix)
	}
	if !jwk.IsPublic() {
		return nil, fmt.Errorf("%s: JWK is not a public key", prefix)
	}
	return &jwk, nil
}

// IsPasswordError checks whether an error is related to a missing or wrong
// password
func IsPasswordError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "password") &&
		(strings.Contains(msg, "missing") || strings.Contains(msg, "wrong"))
}

// ParsePrivateKey tries to parse a private key in DER format first and
// PEM format after, returning an error if the parsing failed
func ParsePrivateKey(privKey, privKeyPassword []byte, prefix string) (interface{}, error) {
	key, err := x509.ParsePKCS8PrivateKey(privKey)
	if err != nil {
		key, err = x509.ParsePKCS1PrivateKey(privKey)
		if err != nil {
			key, err = x509.ParseECPrivateKey(privKey)
		}
	}
	if err != nil {
		block, _ := pem.Decode(privKey)
		if block != nil {
			var der []byte
			if x509.IsEncryptedPEMBlock(block) {
				if privKeyPassword == nil {
					return nil, errors.Errorf("%s: Missing password for encrypted private key", prefix)
				}
				der, err = x509.DecryptPEMBlock(block, privKeyPassword)
				if err != nil {
					return nil, errors.Errorf("%s: Wrong password: could not decrypt private key", prefix)
				}
			} else {
				der = block.Bytes
			}

			key, err = x509.ParsePKCS8PrivateKey(der)
			if err != nil {
				key, err = x509.ParsePKCS1PrivateKey(der)
				if err != nil {
					return nil, errors.Wrapf(err, "%s: Could not parse private key", prefix)
				}
			}
		} else {
			key, err = parseJWKPrivateKey(privKey, prefix)
		}
	}
	return key, err
}

// IsPrivateKey returns true in case the given byte array represents a private key
// It returns an error if for example the password is wrong
func IsPrivateKey(data []byte, password []byte) (bool, error) {
	_, err := ParsePrivateKey(data, password, "")
	return err == nil, err
}

// ParsePublicKey tries to parse a public key in DER format first and
// PEM format after, returning an error if the parsing failed
func ParsePublicKey(pubKey []byte, prefix string) (interface{}, error) {
	key, err := x509.ParsePKIXPublicKey(pubKey)
	if err != nil {
		block, _ := pem.Decode(pubKey)
		if block != nil {
			key, err = x509.ParsePKIXPublicKey(block.Bytes)
			if err != nil {
				return nil, errors.Wrapf(err, "%s: Could not parse public key", prefix)
			}
		} else {
			key, err = parseJWKPublicKey(pubKey, prefix)
		}
	}
	return key, err
}

// IsPublicKey returns true in case the given byte array represents a public key
func IsPublicKey(data []byte) bool {
	_, err := ParsePublicKey(data, "")
	return err == nil
}

// ParseCertificate tries to parse a public key in DER format first and
// PEM format after, returning an error if the parsing failed
func ParseCertificate(certBytes []byte, prefix string) (*x509.Certificate, error) {
	x509Cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		block, _ := pem.Decode(certBytes)
		if block == nil {
			return nil, fmt.Errorf("%s: Could not PEM decode x509 certificate", prefix)
		}
		x509Cert, err = x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.Wrapf(err, "%s: Could not parse x509 certificate", prefix)
		}
	}
	return x509Cert, err
}

// IsCertificate returns true in case the given byte array represents an x.509 certificate
func IsCertificate(data []byte) bool {
	_, err := ParseCertificate(data, "")
	return err == nil
}

// IsGPGPrivateKeyRing returns true in case the given byte array represents a GPG private key ring file
func IsGPGPrivateKeyRing(data []byte) bool {
	r := bytes.NewBuffer(data)
	_, err := openpgp.ReadKeyRing(r)
	return err == nil
}

// SortDecryptionKeys parses a list of comma separated base64 entries and sorts the data into
// a map. Each entry in the list may be either a GPG private key ring, private key, or x.509
// certificate
func SortDecryptionKeys(b64ItemList string) (map[string][][]byte, error) {
	dcparameters := make(map[string][][]byte)

	for _, b64Item := range strings.Split(b64ItemList, ",") {
		var password []byte
		b64Data := strings.Split(b64Item, ":")
		keyData, err := base64.StdEncoding.DecodeString(b64Data[0])
		if err != nil {
			return nil, errors.New("Could not base64 decode a passed decryption key")
		}
		if len(b64Data) == 2 {
			password, err = base64.StdEncoding.DecodeString(b64Data[1])
			if err != nil {
				return nil, errors.New("Could not base64 decode a passed decryption key password")
			}
		}
		var key string
		isPrivKey, err := IsPrivateKey(keyData, password)
		if IsPasswordError(err) {
			return nil, err
		}
		if isPrivKey {
			key = "privkeys"
			if _, ok := dcparameters["privkeys-passwords"]; !ok {
				dcparameters["privkeys-passwords"] = [][]byte{password}
			} else {
				dcparameters["privkeys-passwords"] = append(dcparameters["privkeys-passwords"], password)
			}
		} else if IsCertificate(keyData) {
			key = "x509s"
		} else if IsGPGPrivateKeyRing(keyData) {
			key = "gpg-privatekeys"
		}
		if key != "" {
			values := dcparameters[key]
			if values == nil {
				dcparameters[key] = [][]byte{keyData}
			} else {
				dcparameters[key] = append(dcparameters[key], keyData)
			}
		} else {
			return nil, errors.New("Unknown decryption key type")
		}
	}

	return dcparameters, nil
}

// IsPkcs11SharedLibrary return true in case the given sting represent a real pkcs11 module file path
func IsPkcs11SharedLibrary(module string) bool {
	p := p11.New(strings.TrimSpace(module))
	if p == nil {
		return false
	}
	err := p.Initialize()
	defer p.Destroy()
	defer p.Finalize()
	if err != nil {
		return false
	}
	return true
}

// InputPassword read the password
func InputPassword(name string) (string, error) {
	fmt.Printf("Enter %s Password: ", name)
	bytePassword, err := terminal.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", err
	}
	fmt.Println()
	password := string(bytePassword)
	return strings.TrimSpace(password), nil
}
