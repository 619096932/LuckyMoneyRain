package db

import (
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func NewMySQL(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetConnMaxLifetime(3 * time.Minute)
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	if err := db.Ping(); err != nil {
		return nil, err
	}
	return db, nil
}
