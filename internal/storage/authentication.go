package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
)

// AuthSalt returns the persistent per-database salt used for administrator
// password derivation. It contains no password or fast password verifier.
func (s *Store) AuthSalt(ctx context.Context) (string, error) {
	return WriteValue(ctx, s, func(tx *sql.Tx) (string, error) {
		if _, err := tx.ExecContext(ctx, "INSERT INTO authentication(id,salt) VALUES(1,?) ON CONFLICT(id) DO NOTHING", rand.Text()); err != nil {
			return "", err
		}
		var salt string
		err := tx.QueryRowContext(ctx, "SELECT salt FROM authentication WHERE id=1").Scan(&salt)
		return salt, err
	})
}
