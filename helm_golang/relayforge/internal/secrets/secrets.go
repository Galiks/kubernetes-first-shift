// Package secrets mirrors relayforge/secrets.py: paths to mounted secrets and
// the read-secret helper (strip one trailing newline).
package secrets

import "os"

const (
	ClientTokenPath     = "/etc/relayforge/secrets/client-token"
	SigningKeyPath      = "/etc/relayforge/secrets/signing-key"
	VerificationKeyPath = "/etc/relayforge/secrets/verification-key"
	ControlTokenPath    = "/etc/relayforge/secrets/control-token"
)

func Read(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	for len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	if len(b) > 0 && b[len(b)-1] == '\r' {
		b = b[:len(b)-1]
	}
	return b, nil
}