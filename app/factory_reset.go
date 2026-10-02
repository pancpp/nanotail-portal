package app

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/auth"
	"github.com/pancpp/nanotail-portal/database"
	"github.com/pancpp/nanotail-portal/factoryreset"
	"golang.org/x/crypto/bcrypt"
)

func initFactoryResetAPI(e *echo.Echo, reset *factoryreset.Controller) {
	e.POST("/api/v1/factory-reset", func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		token, ok := c.Get(auth.JWT_CONTEXT_KEY_TOKEN).(*jwt.Token)
		if !ok || token == nil || !token.Valid {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}
		claims, ok := token.Claims.(*auth.Claims)
		if !ok || claims == nil || claims.UserPID <= 0 {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}
		user := &database.User{PID: claims.UserPID}
		if err := database.DB().NewSelect().Model(user).WherePK().Scan(c.Request().Context()); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "Internal Server Error")
		}
		if user.Role != "admin" {
			return echo.NewHTTPError(http.StatusForbidden, "Only portal administrators can perform a factory reset")
		}
		var request struct {
			Confirmed    bool   `json:"confirmed"`
			Confirmation string `json:"confirmation"`
			Password     string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(c.Response(), c.Request().Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || !request.Confirmed || request.Confirmation != "RESET" || request.Password == "" || len(request.Password) > 72 {
			return echo.NewHTTPError(http.StatusBadRequest, "Confirm both reset warnings, type RESET, and enter your current password")
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid reset request")
		}
		if bcrypt.CompareHashAndPassword([]byte(user.Passwd), []byte(request.Password)) != nil {
			return echo.NewHTTPError(http.StatusForbidden, "The current password is incorrect")
		}
		if reset == nil {
			return echo.NewHTTPError(http.StatusServiceUnavailable, "Factory reset is unavailable")
		}
		if err := reset.Accept(); err != nil {
			if errors.Is(err, factoryreset.ErrBusy) {
				return echo.NewHTTPError(http.StatusConflict, err.Error())
			}
			if errors.Is(err, factoryreset.ErrPaths) {
				return echo.NewHTTPError(http.StatusConflict, err.Error())
			}
			log.Printf("(factory reset) preflight failed: %v", err)
			return echo.NewHTTPError(http.StatusConflict, "Factory reset safety checks failed. Check local service logs; no data has been cleared")
		}
		// Acceptance is not completion. Flush it before shutdown/logout can sever
		// this connection. Execution is owned by main, not the request context.
		err := c.JSON(http.StatusAccepted, map[string]bool{"accepted": true})
		_ = http.NewResponseController(c.Response()).Flush()
		reset.Schedule()
		return err
	}, jwtMiddleware())
}
