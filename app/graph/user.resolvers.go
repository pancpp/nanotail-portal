package graph

import (
	"context"
	"time"

	"github.com/pancpp/nanotail-portal/app/auth"
	"github.com/pancpp/nanotail-portal/app/graph/model"
	"github.com/pancpp/nanotail-portal/database"
	"golang.org/x/crypto/bcrypt"
)

func (r *mutationResolver) changePassword(ctx context.Context, passwords model.ChangePassword) (bool, error) {
	ctxVal := queryContextValue(ctx)
	if err := auth.AuthenticateWithUserPIDPassword(ctxVal.UserPID, passwords.Oldpassword); err != nil {
		return false, err
	}

	// bcrypt accepts at most 72 bytes; never silently truncate a password.
	if len(passwords.Newpassword) < 8 || len(passwords.Newpassword) > 72 {
		return false, ErrInvalidPassword
	}

	// hash the new password
	passwdHash, err := bcrypt.GenerateFromPassword([]byte(passwords.Newpassword), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}

	// Update only the authenticated user's password and modification time.
	user := &database.User{
		PID:        ctxVal.UserPID,
		Passwd:     string(passwdHash),
		UpdateTime: time.Now().UTC(),
	}
	result, err := database.DB().NewUpdate().Model(user).WherePK().Column("passwd", "update_time").Exec(ctx)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if changed != 1 {
		return false, auth.ErrUnauthorized
	}

	return true, nil
}

func (r *queryResolver) user(ctx context.Context) (*model.User, error) {
	return nil, nil
}
