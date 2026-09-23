package app

import (
	"errors"
	"log"
	"net/http"

	"github.com/labstack/echo/v5"
	"github.com/pancpp/nanotail-portal/app/auth"
)

func handleLogin(c *echo.Context) error {
	type ReqMsg struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}

	var reqmsg ReqMsg
	if err := c.Bind(&reqmsg); err != nil {
		log.Println("(login) bind request msg err:", err)
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	user, err := auth.AuthenticateWithUsernamePassword(reqmsg.Username, reqmsg.Password)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			return echo.NewHTTPError(http.StatusUnauthorized, auth.ErrUnauthorized.Error())
		} else {
			log.Println("(login) authenticate with username password err: ", err)
			return err
		}
	}

	// successfully login, create token pair
	token, err := auth.CreateJwtToken(user.PID)
	if err != nil {
		log.Println("(login) create token err:", err)
		return err
	}
	type ResMsg struct {
		Token string `json:"token"`
	}

	return c.JSON(http.StatusOK, ResMsg{Token: token})
}
