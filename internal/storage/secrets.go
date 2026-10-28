package storage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"

	"video-rename/internal/domain"
)

const databaseKeySize = 32

var errInvalidDatabaseKey = errors.New("database key file is invalid")

func (s *Store) initSecret(ctx context.Context, path string) error {
	key, err := readSecretKey(path)
	if errors.Is(err, fs.ErrNotExist) {
		var n int
		if err = s.write.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM model_configs WHERE api_key<>'')+(SELECT count(*) FROM runs WHERE model_snapshot<>'')+(SELECT count(*) FROM notification_channels WHERE credentials<>'')").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return errors.New("database key file is missing; restore the matching .key file")
		}
		key = make([]byte, databaseKeySize)
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
		err = errors.Join(err, f.Close())
	}
	if err != nil {
		return err
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

func readSecretKey(path string) (_ []byte, err error) {
	// Nonblocking open lets us reject special files without waiting on a FIFO.
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open database key: %w", err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database key: %w", closeErr))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect database key: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() != databaseKeySize {
		return nil, errInvalidDatabaseKey
	}
	// Recheck the bytes after Stat as another process may resize the file. The
	// extra byte detects oversized keys while keeping allocations bounded.
	key, err := io.ReadAll(io.LimitReader(file, databaseKeySize+1))
	if err != nil {
		return nil, fmt.Errorf("read database key: %w", err)
	}
	if len(key) != databaseKeySize {
		return nil, errInvalidDatabaseKey
	}
	return key, nil
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
		return "", domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonSecretInvalid, Message: "stored secret is malformed"})
	}
	plain, err := s.secret.Open(nil, data[:s.secret.NonceSize()], data[s.secret.NonceSize():], []byte("video-rename-model-v1"))
	if err != nil {
		return "", domain.Fail(domain.KindInternal, domain.Failure{Reason: domain.ReasonSecretUnreadable, Message: "stored secret cannot be decrypted; check the database .key file"})
	}
	return string(plain), nil
}
