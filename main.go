package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v2"
)

var User struct {
	State        string `json:"state"`
	RefreshToken string `json:"refresh_token"`
}

func main() {
	app := fiber.New()
	app.Get("/redirect", func(c *fiber.Ctx) error {
		state := c.Query("client_id") + "and" + c.Query("client_secret")
		url := `https://accounts.zoho.com/oauth/v2/auth?scope=ZohoCliq.Channels.CREATE,ZohoCliq.Channels.READ,ZohoCliq.Channels.UPDATE,ZohoCliq.Channels.DELETE&client_id=` + c.Query("client_id") + `&state=` + state + `&response_type=code&redirect_uri=http://localhost:3000/callback&access_type=offline`
		return c.Redirect(url)
	})
	app.Get("/callback", func(c *fiber.Ctx) error {
		code := c.Query("code")
		state := c.Query("state")
		data := strings.Split(state, "and")

		reqBody := "grant_type=authorization_code" +
			"&client_id=" + data[0] +
			"&client_secret=" + data[1] +
			"&redirect_uri=http://localhost:3000/callback" +
			"&code=" + code
		resp, err := http.Post(
			"https://accounts.zoho.com/oauth/v2/token",
			"application/x-www-form-urlencoded",
			strings.NewReader(reqBody),
		)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		dataByte, _ := io.ReadAll(resp.Body)
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := json.Unmarshal(dataByte, &body); err != nil {
			return c.JSON(fiber.Map{"error": err.Error()})
		}
		fmt.Println(body.RefreshToken)
		return c.SendString(body.RefreshToken)
	})

	app.Listen(":3000")
}
