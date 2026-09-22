package storage

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
)

func (s *Store) initSecret(path string) error {
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		var n int
		if err = s.QueryRow("SELECT (SELECT count(*) FROM model_configs WHERE api_key<>'')+(SELECT count(*) FROM runs WHERE model_snapshot<>'')+(SELECT count(*) FROM notification_channels WHERE credentials<>'')").Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("数据库密钥文件缺失，请恢复对应的 .key 文件")
		}
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return err
		}
		f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
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
		return fmt.Errorf("数据库密钥文件无效")
	}
	if err = os.Chmod(path, 0600); err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	s.secret, err = cipher.NewGCM(block)
	return err
}
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
func (s *Store) Unseal(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(data) < s.secret.NonceSize() {
		return "", fmt.Errorf("模型密钥数据无效")
	}
	plain, err := s.secret.Open(nil, data[:s.secret.NonceSize()], data[s.secret.NonceSize():], []byte("video-rename-model-v1"))
	if err != nil {
		return "", fmt.Errorf("模型密钥无法解密，请核对数据库 .key 文件")
	}
	return string(plain), nil
}
