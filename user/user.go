package user

import (
	"context"
	"log"
	"net/http"

	"github.com/labstack/echo/v5"
)

func Init(ctx context.Context, e *echo.Echo) error {
	e.POST("/api/user/login", handleLogin)
	return nil
}

func handleLogin(c *echo.Context) error {
	// get request
	type ReqMsg struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	var reqmsg ReqMsg
	if err := c.Bind(&reqmsg); err != nil {
		log.Println("(login) bind request msg err:", err)
		return echo.NewHTTPError(http.StatusBadRequest, ErrInvalidRequest.Error())
	}

	// send response
	type ResMsg struct {
		AccessToken string `json:"access_token"`
	}
	resmsg := ResMsg{
		AccessToken: "aaa",
	}
	return c.JSON(http.StatusOK, &resmsg)
}

func handleChangePassword(c *echo.Context) error {
	return nil
}
