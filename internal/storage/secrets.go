package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"

	"video-rename/internal/domain"
)

func (s *Store) initSecret(path string) error {
	key, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		var n int
		if err = s.QueryRow("SELECT (SELECT count(*) FROM model_configs WHERE api_key<>'')+(SELECT count(*) FROM runs WHERE model_snapshot<>'')+(SELECT count(*) FROM notification_channels WHERE credentials<>'')").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errors.New("database key file is missing; restore the matching .key file")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if e != nil {
			return e
		}
		_, err = f.Write(key)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return errors.New("database key file is invalid")
	}
	if err = os.Chmod(path, 0o600); err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	s.secret, err = cipher.NewGCM(block)
	return err
}

// Seal encrypts value for storage; an empty value stays empty.
func (s *Store) Seal(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	nonce := make([]byte, s.secret.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	encrypted := s.secret.Seal(nonce, nonce, []byte(value), []byte("video-rename-model-v1"))
	return base64.StdEncoding.EncodeToString(encrypted), nil
}

// Unseal decrypts a value produced by Seal.
func (s *Store) Unseal(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) < s.secret.NonceSize() {
		return "", domain.Fail(domain.KindInternal, "secret_invalid", "stored secret is malformed")
	}
	plain, err := s.secret.Open(nil, data[:s.secret.NonceSize()], data[s.secret.NonceSize():], []byte("video-rename-model-v1"))
	if err != nil {
		return "", domain.Fail(domain.KindInternal, "secret_unreadable", "stored secret cannot be decrypted; check the database .key file")
	}
	return string(plain), nil
}
