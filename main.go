package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/EthicalGopher/AfterWork_Buddy/db"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
)

func init() {
	err := godotenv.Load()
	if err != nil {
		panic(err)
	}
}

var hostUrl = "https://afterwork-buddy.onrender.com"

func runTimer(email string, timezone string) error {
	isDaily := true
	for isDaily {
		userTime, err := db.GetTimer(email)
		if err != nil {
			return err
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return err
		}

		now := time.Now()
		thisTime := now.In(loc)
		currenttime := thisTime.Format("03:04 PM")
		if userTime.StartTime == currenttime {
			//mute function
			time.Sleep(time.Duration(userTime.Duration) * time.Minute)
			//unmute function
			break
		}
	}
	return nil
}
func server() {
	app := fiber.New()
	app.Get("/redirect", func(c *fiber.Ctx) error {
		state := c.Query("client_id") + "and" + c.Query("client_secret") + "and" + c.Query("email")
		url := `https://accounts.zoho.com/oauth/v2/auth?scope=ZohoCliq.Chats.UPDATE,ZohoCliq.Channels.CREATE,ZohoCliq.Channels.READ,ZohoCliq.Channels.UPDATE,ZohoCliq.Channels.DELETE&client_id=` + c.Query("client_id") + `&state=` + state + `&response_type=code&redirect_uri=` + hostUrl + `/callback&access_type=offline`
		return c.Redirect(url)
	})
	app.Get("/callback", func(c *fiber.Ctx) error {
		code := c.Query("code")
		state := c.Query("state")
		data := strings.Split(state, "and")

		reqBody := "grant_type=authorization_code" +
			"&client_id=" + data[0] +
			"&client_secret=" + data[1] +
			"&redirect_uri=" + hostUrl + "/callback" +
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
		fmt.Println(string(dataByte))
		var body db.User
		body.Email = data[2]
		body.State = state
		if err := json.Unmarshal(dataByte, &body); err != nil {
			return c.JSON(fiber.Map{"error": err.Error()})
		}
		fmt.Println(body.RefreshToken)
		if err := body.AddUser(); err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusAccepted).SendString("Success")
	})
	app.Get("/gettoken", func(c *fiber.Ctx) error {
		email := c.Query("email")
		body, err := db.GetRefreshToken(email)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}
		var newBody struct {
			AccessToken string `json:"access_token"`
		}
		data := strings.Split(body.State, "and")
		reqBody := `refresh_token=` + body.RefreshToken + `&grant_type=refresh_token&scope=ZohoCliq.Chats.UPDATE,ZohoCliq.Channels.CREATE,ZohoCliq.Channels.READ,ZohoCliq.Channels.UPDATE,ZohoCliq.Channels.DELETE&client_id=` + data[0] + `&client_secret=` + data[1] + `&redirect_uri=` + hostUrl + `/callback`
		resp, err := http.Post(
			"https://accounts.zoho.com/oauth/v2/token",
			"application/x-www-form-urlencoded",
			strings.NewReader(reqBody),
		)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		dataByte, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(dataByte, &newBody); err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusAccepted).JSON(newBody)
	})
	app.Post("/settimer", func(c *fiber.Ctx) error {
		email := c.Query("email")
		timezone := c.Query("timezone")
		starttime := c.Query("starttime")
		duration := c.Query("duration")
		isDaily := c.Query("isDaily")
		var err error
		var timer db.Timer
		timer.Duration, err = strconv.Atoi(duration)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(fmt.Sprintf("%v", err.Error()))
		}
		timer.StartTime = starttime
		timer.IsDaily, err = strconv.ParseBool(isDaily)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).SendString(fmt.Sprintf("%v", err.Error()))
		}
		err = db.SaveTimer(email, timer)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).SendString(fmt.Sprintf("%v", err.Error()))
		}
		go runTimer(email, timezone)
		return c.SendStatus(fiber.StatusCreated)
	})
	app.Listen(":3000")
}
func main() {
	db.Connect()
	server()
	defer db.Disconnect()

}
