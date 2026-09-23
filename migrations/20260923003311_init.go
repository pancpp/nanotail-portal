package migrations

import (
	"context"
	"fmt"
	"log"

	"github.com/pancpp/fairnet-portal/database"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

func init() {
	Migrations.MustRegister(func(ctx context.Context, db *bun.DB) error {
		fmt.Print(" [up migration] ")
		fmt.Println("create table: User")
		if _, err := db.NewCreateTable().Model((*database.User)(nil)).IfNotExists().Exec(ctx); err != nil {
			return err
		}
		if err := createDefaultUser(ctx, db); err != nil {
			return err
		}
		return nil
	}, func(ctx context.Context, db *bun.DB) error {
		fmt.Print(" [do not support roll back for safety purpose] ")
		return nil
	})
}

func createDefaultUser(ctx context.Context, db *bun.DB) error {
	const (
		DEFAULT_USERNAME = "admin"
		DEFAULT_PASSWORD = "admin"
	)

	passwdHash, err := bcrypt.GenerateFromPassword([]byte(DEFAULT_PASSWORD), bcrypt.DefaultCost)
	if err != nil {
		log.Println("(migrations) generate default password hash err:", err)
		return err
	}
	user := &database.User{
		Username: DEFAULT_USERNAME,
		Passwd:   string(passwdHash),
		Role:     "admin",
	}
	if _, err := db.NewInsert().Model(user).Exec(ctx); err != nil {
		log.Println("(migrations) insert default user err:", err)
		return err
	}

	return nil
}
