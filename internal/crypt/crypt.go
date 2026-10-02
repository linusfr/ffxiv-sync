// Package crypt encrypts blobs before they leave the machine. Plugin settings
// hold API tokens and push keys, and a store is a folder on someone else's disk
// or a server on the internet.
package crypt

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

// Prefix marks an encrypted blob, so a store holding both can tell them apart.
var prefix = []byte("ffsync1\x00")

const (
	saltLength = 16
	keyLength  = 32

	// Iterations is the cost of deriving the key. It is paid once per blob, and
	// a push touches a handful of files.
	iterations = 600_000
)

// ErrNoPassphrase is returned when an encrypted blob is read without one.
var ErrNoPassphrase = errors.New("this store is encrypted; set a passphrase")

// Encrypted reports whether a blob was sealed by this package.
func Encrypted(blob []byte) bool {
	return len(blob) > len(prefix) && string(blob[:len(prefix)]) == string(prefix)
}

// Sealer encrypts a run's blobs. Deriving the key is deliberately slow, so it
// happens once per run rather than once per file: a push of 150 settings files
// would otherwise spend most of a minute on it. Each blob still gets its own
// nonce, and the salt travels in every blob so a reader needs nothing but the
// passphrase.
type Sealer struct {
	salt []byte
	aead cipher.AEAD
}

// NewSealer derives the key once.
func NewSealer(passphrase string) (*Sealer, error) {
	if passphrase == "" {
		return nil, ErrNoPassphrase
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

	aead, err := sealer(passphrase, salt)
	if err != nil {
		return nil, err
	}

	return &Sealer{salt: salt, aead: aead}, nil
}

// Seal encrypts one blob.
func (s *Sealer) Seal(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	sealed := make([]byte, 0, len(prefix)+len(s.salt)+len(nonce)+len(plain)+s.aead.Overhead())
	sealed = append(sealed, prefix...)
	sealed = append(sealed, s.salt...)
	sealed = append(sealed, nonce...)

	return s.aead.Seal(sealed, nonce, plain, nil), nil
}

// Opener decrypts blobs, keeping the derived key for each salt it meets: one
// push writes every blob with the same salt, so a pull derives once too.
type Opener struct {
	passphrase string
	keys       map[string]cipher.AEAD
}

// NewOpener returns an opener; an empty passphrase is allowed, and only fails
// if an encrypted blob actually turns up.
func NewOpener(passphrase string) *Opener {
	return &Opener{passphrase: passphrase, keys: map[string]cipher.AEAD{}}
}

// Open decrypts a blob, and passes a plain one through untouched.
func (o *Opener) Open(sealed []byte) ([]byte, error) {
	if !Encrypted(sealed) {
		return sealed, nil
	}
	if o.passphrase == "" {
		return nil, ErrNoPassphrase
	}

	body := sealed[len(prefix):]
	if len(body) < saltLength {
		return nil, fmt.Errorf("blob is too short to be encrypted")
	}
	salt, body := body[:saltLength], body[saltLength:]

	aead, known := o.keys[string(salt)]
	if !known {
		var err error
		if aead, err = sealer(o.passphrase, salt); err != nil {
			return nil, err
		}
		o.keys[string(salt)] = aead
	}

	if len(body) < aead.NonceSize() {
		return nil, fmt.Errorf("blob is too short to be encrypted")
	}

	nonce, body := body[:aead.NonceSize()], body[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, body, nil)
	if err != nil {
		return nil, fmt.Errorf("wrong passphrase, or the blob is damaged")
	}

	return plain, nil
}

// Seal is the one-off form, for a single blob.
func Seal(passphrase string, plain []byte) ([]byte, error) {
	s, err := NewSealer(passphrase)
	if err != nil {
		return nil, err
	}

	return s.Seal(plain)
}

// Open is the one-off form, for a single blob.
func Open(passphrase string, sealed []byte) ([]byte, error) {
	return NewOpener(passphrase).Open(sealed)
}

func sealer(passphrase string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, keyLength)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	return cipher.NewGCM(block)
}
