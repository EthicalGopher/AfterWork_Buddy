package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
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
	hostUrl = "https://afterwork-buddy.onrender.com"
	// runningTimers and timersMutex are removed as per new job-based system
)

func muteChannel(accessToken string, channelId string) error {
	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://cliq.zoho.com/api/v2/chats/"+channelId+"/mute", nil)
	if err != nil {
		log.Printf("Error creating mute request: %v", err)
		return err
	}
	req.Header.Add("Authorization", "Zoho-oauthtoken "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending mute request: %v", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		err = fmt.Errorf("failed to mute channel %s: status %d, body: %s", channelId, resp.StatusCode, string(bodyBytes))
		log.Print(err)
		return err
	}
	log.Printf("Successfully muted channel %s", channelId)
	return nil
}

func unmuteChannel(accessToken string, channelId string) error {
	client := &http.Client{}
	req, err := http.NewRequest("POST", "https://cliq.zoho.com/api/v2/chats/"+channelId+"/unmute", nil)
	if err != nil {
		log.Printf("Error creating unmute request: %v", err)
		return err
	}
	req.Header.Add("Authorization", "Zoho-oauthtoken "+accessToken)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error sending unmute request: %v", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		err = fmt.Errorf("failed to unmute channel %s: status %d, body: %s", channelId, resp.StatusCode, string(bodyBytes))
		log.Print(err)
		return err
	}
	log.Printf("Successfully unmuted channel %s", channelId)
	return nil
}

func refreshAccessToken(email string) (string, error) {
	body, err := db.GetRefreshToken(email)
	if err != nil {
		return "", fmt.Errorf("error getting refresh token for %s: %w", email, err)
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
		return "", fmt.Errorf("error posting refresh token request: %w", err)
	}
	defer resp.Body.Close()
	dataByte, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(dataByte, &newBody); err != nil {
		return "", fmt.Errorf("error unmarshalling refresh token response: %w", err)
	}
	if newBody.Error != "" {
		return "", fmt.Errorf("error refreshing token for %s: %s", email, newBody.Error)
	}
	return newBody.AccessToken, nil
}

// executeJob performs the actual mute/unmute action and marks the job complete
func executeJob(job db.Job) {
	log.Printf("Executing job ID %s: Type=%s, Channel=%s, User=%s", job.ID, job.TaskType, job.ChannelID, job.Email)

	accessToken, err := refreshAccessToken(job.Email)
	if err != nil {
		log.Printf("Failed to refresh access token for user %s, job %s: %v", job.Email, job.ID, err)
		// Optionally, re-schedule the job for a retry later, or mark as failed
		return
	}

	var actionErr error
	if job.TaskType == "MUTE" {
		actionErr = muteChannel(accessToken, job.ChannelID)
	} else if job.TaskType == "UNMUTE" {
		actionErr = unmuteChannel(accessToken, job.ChannelID)
	} else {
		log.Printf("Unknown job type for job %s: %s", job.ID, job.TaskType)
		_ = db.CompleteJob(job.ID) // Mark as complete to avoid re-processing unknown types
		return
	}

	if actionErr != nil {
		log.Printf("Failed to perform %s action for job %s (channel %s, user %s): %v", job.TaskType, job.ID, job.ChannelID, job.Email, actionErr)
		// Optionally, handle retry logic here (e.g., update job status to "FAILED" and retry count)
	} else {
		err = db.CompleteJob(job.ID)
		if err != nil {
			log.Printf("Failed to mark job %s as complete: %v", job.ID, err)
		}
	}
}

// scheduleJob sets a timer to execute a job at its scheduled time
func scheduleJob(job db.Job) {
	duration := time.Until(job.ExecuteAt)
	if duration < 0 { // If job is in the past, execute immediately
		log.Printf("Job %s (Type: %s, Channel: %s) is in the past (due %s). Executing immediately.", job.ID, job.TaskType, job.ChannelID, job.ExecuteAt.Format(time.RFC3339))
		go executeJob(job)
		return
	}

	log.Printf("Scheduling job %s (Type: %s, Channel: %s) to run in %v at %s", job.ID, job.TaskType, job.ChannelID, duration, job.ExecuteAt.Format(time.RFC3339))
	time.AfterFunc(duration, func() {
		executeJob(job)
	})
}

// recoverAndScheduleJobs is run at server startup to process pending jobs.
func recoverAndScheduleJobs() {
	log.Println("--- Starting job recovery and scheduling ---")
	pendingJobs, err := db.GetPendingJobs()
	if err != nil {
		log.Printf("Error getting pending jobs: %v", err)
		return
	}

	if len(pendingJobs) == 0 {
		log.Println("No pending jobs found.")
		return
	}

	log.Printf("Found %d pending jobs. Rescheduling...", len(pendingJobs))
	for _, job := range pendingJobs {
		scheduleJob(job)
	}
	log.Println("--- Job recovery and scheduling complete ---")
}

// startAllUserTimers iterates through all user-defined timers and schedules jobs for them.
// This function needs to be intelligent about not scheduling duplicate jobs for daily timers.
func startAllUserTimers() {
	log.Println("--- Starting initial timer evaluation and job generation ---")
	users, err := db.GetAllUsers()
	if err != nil {
		log.Printf("Error getting all users for timer evaluation: %v", err)
		return
	}

	for _, user := range users {
		for _, timer := range user.Timers {
			loc, err := time.LoadLocation("Asia/Kolkata") // Assuming a default/user-configured timezone
			if err != nil {
				log.Printf("Error loading timezone for user %s, timer %s: %v", user.Email, timer.ID, err)
				continue
			}

			// Parse the start time from the timer
			parsedStartTime, err := time.Parse("15:04", timer.StartTime)
			if err != nil {
				log.Printf("Error parsing timer start time %s for user %s, timer %s: %v", timer.StartTime, user.Email, timer.ID, err)
				continue
			}

			now := time.Now().In(loc)
			// Construct today's mute time
			todayMuteTime := time.Date(now.Year(), now.Month(), now.Day(), parsedStartTime.Hour(), parsedStartTime.Minute(), 0, 0, loc)

			// Calculate unmute time based on mute time and duration
			unmuteTime := todayMuteTime.Add(time.Duration(timer.Duration) * time.Minute)

			// Adjust times for daily timers if already passed today
			if timer.IsDaily && unmuteTime.Before(now) {
				todayMuteTime = todayMuteTime.Add(24 * time.Hour)
				unmuteTime = unmuteTime.Add(24 * time.Hour)
			} else if !timer.IsDaily && unmuteTime.Before(now) {
				log.Printf("Skipping one-time timer %s for user %s as its unmute time (%s) is already past.", timer.ID, user.Email, unmuteTime.Format(time.RFC3339))
				continue // Skip one-time timers that are entirely in the past
			}

			// Iterate through channels and schedule jobs
			for _, channel := range timer.Channels {
				// Schedule MUTE job
				muteJobID := fmt.Sprintf("%s-%s-MUTE-%s", timer.ID, channel, todayMuteTime.Format("20060102"))
				muteJob := db.Job{
					ID:        muteJobID,
					Email:     user.Email,
					TaskType:  "MUTE",
					ChannelID: channel,
					ExecuteAt: todayMuteTime,
					Status:    "PENDING",
					TimerID:   timer.ID,
				}
				if err := db.ScheduleJob(&muteJob); err != nil {
					// Handle duplicate key error gracefully if jobs already exist (e.g., from a previous run)
					log.Printf("Could not schedule MUTE job %s (might already exist or DB error): %v", muteJobID, err)
				} else {
					log.Printf("Scheduled MUTE job %s for %s", muteJobID, todayMuteTime.Format(time.RFC3339))
					scheduleJob(muteJob) // Schedule for in-memory execution
				}

				// Schedule UNMUTE job
				unmuteJobID := fmt.Sprintf("%s-%s-UNMUTE-%s", timer.ID, channel, unmuteTime.Format("20060102"))
				unmuteJob := db.Job{
					ID:        unmuteJobID,
					Email:     user.Email,
					TaskType:  "UNMUTE",
					ChannelID: channel,
					ExecuteAt: unmuteTime,
					Status:    "PENDING",
					TimerID:   timer.ID,
				}
				if err := db.ScheduleJob(&unmuteJob); err != nil {
					log.Printf("Could not schedule UNMUTE job %s (might already exist or DB error): %v", unmuteJobID, err)
				} else {
					log.Printf("Scheduled UNMUTE job %s for %s", unmuteJobID, unmuteTime.Format(time.RFC3339))
					scheduleJob(unmuteJob) // Schedule for in-memory execution
				}
			}
		}
	}
	log.Println("--- Initial timer evaluation and job generation complete ---")
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
		timezoneStr := c.Query("timezone") // Renamed to avoid conflict with time.Location
		starttime := c.Query("start_timer")
		durationStr := c.Query("duration") // Renamed to avoid conflict with time.Duration
		isDailyStr := c.Query("isdaily")   // Renamed to avoid conflict with bool
		channelsBytes := c.Context().QueryArgs().PeekMulti("channels")
		channels := make([]string, len(channelsBytes))
		for i, v := range channelsBytes {
			channels[i] = string(v)
		}

		var timer db.Timing
		var err error

		timer.ID = uuid.New().String()
		timer.Channels = channels
		timer.Duration, err = strconv.Atoi(durationStr)
		if err != nil {
			log.Printf("Error parsing duration: %v", err)
			return c.Status(fiber.StatusBadRequest).SendString("Invalid duration")
		}
		timer.StartTime = starttime
		timer.IsDaily, err = strconv.ParseBool(isDailyStr)
		if err != nil {
			log.Printf("Error parsing isDaily: %v", err)
			return c.Status(fiber.StatusBadRequest).SendString("Invalid isDaily value")
		}

		if err := db.SaveTimer(email, timer); err != nil {
			log.Printf("Error saving timer to DB: %v", err)
			return c.Status(fiber.StatusInternalServerError).SendString(err.Error())
		}

		// Calculate job times and schedule them
		loc, err := time.LoadLocation(timezoneStr)
		if err != nil {
			log.Printf("Error loading timezone %s for user %s: %v", timezoneStr, email, err)
			return c.Status(fiber.StatusInternalServerError).SendString("Invalid timezone")
		}

		parsedStartTime, err := time.Parse("15:04", timer.StartTime)
		if err != nil {
			log.Printf("Error parsing timer start time %s for user %s, timer %s: %v", timer.StartTime, email, timer.ID, err)
			return c.Status(fiber.StatusBadRequest).SendString("Invalid start_timer format")
		}

		now := time.Now().In(loc)
		todayMuteTime := time.Date(now.Year(), now.Month(), now.Day(), parsedStartTime.Hour(), parsedStartTime.Minute(), 0, 0, loc)
		unmuteTime := todayMuteTime.Add(time.Duration(timer.Duration) * time.Minute)

		// If the mute/unmute times are already past for today, and it's a daily timer, schedule for tomorrow.
		// For one-time timers, if it's past, don't schedule.
		if unmuteTime.Before(now) {
			if timer.IsDaily {
				todayMuteTime = todayMuteTime.Add(24 * time.Hour)
				unmuteTime = unmuteTime.Add(24 * time.Hour)
			} else {
				log.Printf("Not scheduling one-time timer %s for user %s as its time is already past for today.", timer.ID, email)
				return c.Status(fiber.StatusOK).JSON(fiber.Map{"message": "One-time timer not scheduled, time already past."})
			}
		}

		for _, channel := range timer.Channels {
			// Schedule MUTE job
			muteJobID := fmt.Sprintf("%s-%s-MUTE-%s", timer.ID, channel, todayMuteTime.Format("20060102"))
			muteJob := db.Job{
				ID:        muteJobID,
				Email:     email,
				TaskType:  "MUTE",
				ChannelID: channel,
				ExecuteAt: todayMuteTime,
				Status:    "PENDING",
				TimerID:   timer.ID,
			}
			if err := db.ScheduleJob(&muteJob); err != nil {
				log.Printf("Error scheduling MUTE job %s: %v", muteJobID, err)
			} else {
				scheduleJob(muteJob)
			}

			// Schedule UNMUTE job
			unmuteJobID := fmt.Sprintf("%s-%s-UNMUTE-%s", timer.ID, channel, unmuteTime.Format("20060102"))
			unmuteJob := db.Job{
				ID:        unmuteJobID,
				Email:     email,
				TaskType:  "UNMUTE",
				ChannelID: channel,
				ExecuteAt: unmuteTime,
				Status:    "PENDING",
				TimerID:   timer.ID,
			}
			if err := db.ScheduleJob(&unmuteJob); err != nil {
				log.Printf("Error scheduling UNMUTE job %s: %v", unmuteJobID, err)
			} else {
				scheduleJob(unmuteJob)
			}
		}

		return c.Status(fiber.StatusCreated).JSON(timer)
	})
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})
	app.Post("/stoptimer", func(c *fiber.Ctx) error {
		email := c.Query("email")
		id := c.Query("id") // This is the timer.ID

		if err := db.RemoveTimer(email, id); err != nil {
			log.Printf("Error removing timer %s for user %s: %v", id, email, err)
			return c.Status(fiber.StatusInternalServerError).SendString("Failed to stop timer")
		}
		log.Printf("Successfully stopped timer %s for user %s", id, email)
		return c.SendStatus(fiber.StatusAccepted)
	})
	app.Listen(":3000")
}

func main() {
	db.Connect()
	// First, recover any jobs that might have been pending from a crash
	recoverAndScheduleJobs()
	// Then, start/schedule new jobs based on user-defined timers (for the current day)
	startAllUserTimers()

	// A goroutine to periodically schedule daily timers for the next day
	go func() {
		ticker := time.NewTicker(1 * time.Hour) // Check every hour
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			// For example, if it's past midnight, schedule jobs for the new day
			// This check prevents running `startAllUserTimers` multiple times right after midnight.
			if now.Hour() == 0 && now.Minute() >= 0 && now.Minute() < 5 { // Run once shortly after midnight
				log.Println("It's a new day! Re-evaluating daily timers.")
				startAllUserTimers()
			}
		}
	}()

	server()
	defer db.Disconnect()
}
