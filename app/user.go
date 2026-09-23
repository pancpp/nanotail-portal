package app

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/fairnet-portal/database"
	"github.com/pancpp/fairnet-portal/internal/api"
	"golang.org/x/crypto/bcrypt"
)

func handleLogin(c *echo.Context) error {
	type ReqMsg struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	var reqmsg ReqMsg
	if err := c.Bind(&reqmsg); err != nil {
		log.Println("(login) bind request msg err:", err)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid username or password")
	}

	user, err := AuthenticateWithUsernamePassword(reqmsg.Username, reqmsg.Password)
	if err != nil {
		if errors.Is(err, ErrUnauthorized) {
			return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
		} else {
			log.Println("(login) authenticate with username password err: ", err)
			return err
		}
	}

	// successfully login, create token pair
	token, err := createJwtToken(user.PID)
	if err != nil {
		log.Println("(login) create token err:", err)
		return err
	}
	type ResMsg struct {
		Token string `json:"token"`
	}

	return c.JSON(http.StatusOK, ResMsg{Token: token})
}

func handleChangePassword(c *echo.Context) error {
	token, ok := c.Get(JWT_CONTEXT_KEY_TOKEN).(*jwt.Token)
	if !ok || token == nil || !token.Valid {
		return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || claims == nil || claims.UserPID <= 0 {
		return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
	}

	var reqmsg struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := api.DecodeJSON(c, &reqmsg); err != nil {
		return err
	}
	if reqmsg.CurrentPassword == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Current password is required")
	}
	// bcrypt accepts at most 72 bytes; never silently truncate a password.
	if len(reqmsg.NewPassword) < 8 || len(reqmsg.NewPassword) > 72 {
		return echo.NewHTTPError(http.StatusBadRequest, ErrInvalidPassword.Error())
	}

	db := database.DB()
	ctx := c.Request().Context()
	user := new(database.User)
	if err := db.NewSelect().Model(user).Where("pid = ?", claims.UserPID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
		}
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(user.Passwd), []byte(reqmsg.CurrentPassword)); err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "Current password is incorrect")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(reqmsg.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// Update only the authenticated user's password and modification time.
	result, err := db.NewUpdate().Model((*database.User)(nil)).
		Set("passwd = ?", string(hash)).
		Set("update_time = ?", time.Now().UTC()).
		Where("pid = ?", user.PID).
		Exec(ctx)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return echo.NewHTTPError(http.StatusUnauthorized, ErrUnauthorized.Error())
	}
	return c.NoContent(http.StatusNoContent)
}
