package database

import (
	"database/sql"
	"fmt"

	// import go libpq driver package
	_ "github.com/lib/pq"
)

// Connect opens a connection to Postgres database and returns a pointer to database.DB
func Connect(cfg *Config) (*Instance, error) {
	if cfg == nil {
		return nil, ErrNilConfig
	}
	if !cfg.Enabled {
		return nil, ErrDatabaseSupportDisabled
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}

	configDSN := fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.Username,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Database,
		cfg.SSLMode)

	db, err := sql.Open(DBPostgreSQL, configDSN)
	if err != nil {
		return nil, err
	}
	err = DB.SetPostgresConnection(db)
	if err != nil {
		return nil, err
	}
	return DB, nil
}
