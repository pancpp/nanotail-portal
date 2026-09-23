package app

import (
	"errors"
	"log"
	"net/http"

	"github.com/labstack/echo/v5"
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
	return nil
}
