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

func muteChannel(accessToken string, channelId string) error {
	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://cliq.zoho.com/api/v2/channels/"+channelId+"/mute", nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Zoho-oauthtoken "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// handle error
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to mute channel %s: status %d, body: %s", channelId, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

func unmuteChannel(accessToken string, channelId string) error {
	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://cliq.zoho.com/api/v2/channels/"+channelId+"/unmute", nil)
	if err != nil {
		return err
	}
	req.Header.Add("Authorization", "Zoho-oauthtoken "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		// handle error
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to unmute channel %s: status %d, body: %s", channelId, resp.StatusCode, string(bodyBytes))
	}
	return nil
}

func refreshAccessToken(email string) (string, error) {
	body, err := db.GetRefreshToken(email)
	if err != nil {
		return "", err
	}
	fmt.Println("RefreshToken : ", body.RefreshToken)
	var newBody struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	data := strings.Split(body.State, "and")
	reqBody := `refresh_token=` + body.RefreshToken + `&grant_type=refresh_token&scope=ZohoCliq.Chats.UPDATE,ZohoCliq.Channels.CREATE,ZohoCliq.Channels.READ,ZohoCliq.Channels.UPDATE,ZohoCliq.Channels.DELETE&client_id=` + data[0] + `&client_secret=` + data[1] + `&redirect_uri=` + hostUrl + `/callback`
	resp, err := http.Post(
		"https://accounts.zoho.com/oauth/v2/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(reqBody),
	)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	dataByte, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(dataByte, &newBody); err != nil {
		return "", err
	}
	if newBody.Error != "" {
		return "", fmt.Errorf("error refreshing token: %s", newBody.Error)
	}
	return newBody.AccessToken, nil
}
func runTimer(email string, timezone string, channels []string) error {
	for {
		// read DB timer settings *every loop*
		userTime, err := db.GetTimer(email)
		if err != nil {
			// user removed timer → stop goroutine
			return nil
		}

		if !userTime.IsDaily {
			// if not daily → run only once
			return nil
		}

		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return err
		}

		now := time.Now().In(loc)
		currenttime := now.Format("3:04 PM")

		if userTime.StartTime == currenttime {

			// ------------------- MUTE -------------------
			accessToken, err := refreshAccessToken(email)
			if err == nil {
				for _, ch := range channels {
					_ = muteChannel(accessToken, ch)
				}
			}

			// wait duration
			time.Sleep(time.Duration(userTime.Duration) * time.Minute)

			// ------------------- UNMUTE -------------------
			accessToken, err = refreshAccessToken(email)
			if err == nil {
				for _, ch := range channels {
					_ = unmuteChannel(accessToken, ch)
				}
			}

			// if not daily → stop after 1 cycle
			if !userTime.IsDaily {
				return nil
			}
		}

		time.Sleep(30 * time.Second)
	}
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
		accessToken, err := refreshAccessToken(email)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"access_token": accessToken})
	})
	app.Post("/settimer", func(c *fiber.Ctx) error {
		email := c.Query("email")
		timezone := c.Query("timezone")
		starttime := c.Query("starttime")
		duration := c.Query("duration")
		channelsBytes := c.Context().QueryArgs().PeekMulti("channels")
		channels := make([]string, len(channelsBytes))
		for i, v := range channelsBytes {
			channels[i] = string(v)
		}
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
		go runTimer(email, timezone, channels)
		return c.SendStatus(fiber.StatusCreated)
	})
	app.Post("/stoptimer", func(c *fiber.Ctx) error {
		email := c.Query("email")
		if err := db.RemoveTimer(email); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendStatus(fiber.StatusAccepted)

	})
	app.Listen(":3000")
}
func main() {
	db.Connect()
	server()
	defer db.Disconnect()

}
