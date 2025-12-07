package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/EthicalGopher/AfterWork_Buddy/db"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
)

func init() {
	err := godotenv.Load()
	if err != nil {
		panic(err)
	}
}

var (
	hostUrl       = "https://afterwork-buddy.onrender.com"
	runningTimers = make(map[string]context.CancelFunc)
	timersMutex   = &sync.Mutex{}
)

func muteChannel(accessToken string, channelId string) error {
	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://cliq.zoho.com/api/v2/chats/"+channelId+"/mute", nil)
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
	req, err := http.NewRequest("POST", "https://cliq.zoho.com/api/v2/chats/"+channelId+"/unmute", nil)
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
func runTimer(ctx context.Context, email string, timezone string, timer db.Timing) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			loc, err := time.LoadLocation(timezone)
			if err != nil {
				fmt.Printf("error loading location for timer %s: %v\n", timer.ID, err)
				return
			}

			now := time.Now().In(loc)
			currenttime := now.Format("15:04")
			if timer.StartTime == currenttime {
				accessToken, err := refreshAccessToken(email)
				if err == nil {
					for _, ch := range timer.Channels {
						_ = muteChannel(accessToken, ch)
					}
				}

				time.Sleep(time.Duration(timer.Duration) * time.Minute)

				accessToken, err = refreshAccessToken(email)
				if err == nil {
					for _, ch := range timer.Channels {
						_ = unmuteChannel(accessToken, ch)
					}
				}

				if !timer.IsDaily {
					return
				}
			}

			time.Sleep(30 * time.Second)
		}
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
		fmt.Println(email)
		accessToken, err := refreshAccessToken(email)
		if err != nil {
			return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"access_token": accessToken})
	})
	app.Post("/settimer", func(c *fiber.Ctx) error {
		email := c.Query("email")
		timezone := c.Query("timezone")
		starttime := c.Query("start_timer")
		duration := c.Query("duration")
		isDaily := c.Query("isdaily")
		channelsBytes := c.Context().QueryArgs().PeekMulti("channels")
		channels := make([]string, len(channelsBytes))
		for i, v := range channelsBytes {
			channels[i] = string(v)
		}

		var timer db.Timing
		var err error

		timer.ID = uuid.New().String()
		timer.Channels = channels
		timer.Duration, err = strconv.Atoi(duration)
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}
		timer.StartTime = starttime
		timer.IsDaily, err = strconv.ParseBool(isDaily)
		if err != nil {
			return c.Status(500).SendString(err.Error())
		}

		if err := db.SaveTimer(email, timer); err != nil {
			return c.Status(500).SendString(err.Error())
		}

		ctx, cancel := context.WithCancel(context.Background())
		timersMutex.Lock()
		runningTimers[timer.ID] = cancel
		timersMutex.Unlock()

		go runTimer(ctx, email, timezone, timer)
		return c.Status(201).JSON(timer)
	})
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	app.Post("/stoptimer", func(c *fiber.Ctx) error {
		email := c.Query("email")
		id := c.Query("id")

		timersMutex.Lock()
		cancel, ok := runningTimers[id]
		if ok {
			cancel()
			delete(runningTimers, id)
		}
		timersMutex.Unlock()

		if err := db.RemoveTimer(email, id); err != nil {
			return c.SendStatus(fiber.StatusInternalServerError)
		}
		return c.SendStatus(fiber.StatusAccepted)
	})
	app.Listen(":3000")
}
func startAllTimers() {
	users, err := db.GetAllUsers()
	if err != nil {
		fmt.Printf("could not get users: %v", err)
		return
	}

	for _, user := range users {
		for _, timer := range user.Timers {
			ctx, cancel := context.WithCancel(context.Background())
			timersMutex.Lock()
			runningTimers[timer.ID] = cancel
			timersMutex.Unlock()
			go runTimer(ctx, user.Email, "Asia/Kolkata", timer) // assuming timezone
		}
	}
}

func main() {
	db.Connect()
	startAllTimers()
	server()
	defer db.Disconnect()

}
