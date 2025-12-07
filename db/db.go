package db

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

var client *mongo.Client

// ------------------- DATA MODELS -------------------

// Job represents a single, persistent task to be executed.
type Job struct {
	ID        string    `json:"id"        bson:"_id"`
	Email     string    `json:"email"     bson:"email"`
	TaskType  string    `json:"task_type" bson:"task_type"` // "MUTE" or "UNMUTE"
	ChannelID string    `json:"channel_id" bson:"channel_id"`
	ExecuteAt time.Time `json:"execute_at" bson:"execute_at"`
	Status    string    `json:"status"    bson:"status"` // "PENDING", "COMPLETE"
	TimerID   string    `json:"timer_id"  bson:"timer_id"`
}

type Timing struct {
	ID        string   `json:"id" bson:"id"`
	StartTime string   `json:"starttime" bson:"starttime"`
	Duration  int      `json:"duration"  bson:"duration"`
	IsDaily   bool     `json:"isdaily"   bson:"isdaily"`
	Channels  []string `json:"channels"  bson:"channels"`
}

type User struct {
	Email        string   `json:"email"         bson:"email"`
	RefreshToken string   `json:"refresh_token" bson:"refresh_token"`
	State        string   `json:"state"         bson:"state"`
	Timers       []Timing `json:"timers"        bson:"timers"`
}

// ------------------- CONNECTION -------------------

func Connect() {
	serverAPI := options.ServerAPI(options.ServerAPIVersion1)
	uri := os.Getenv("MONGO_URI")
	opts := options.Client().ApplyURI(uri).SetServerAPIOptions(serverAPI)

	var err error
	client, err = mongo.Connect(opts)
	if err != nil {
		panic(err)
	}

	if err := client.Ping(context.TODO(), readpref.Primary()); err != nil {
		panic(err)
	}

	fmt.Println("Connected to MongoDB!")
}

func Disconnect() {
	if err := client.Disconnect(context.TODO()); err != nil {
		panic(err)
	}
}

// ------------------- USER CRUD -------------------
func (u *User) AddUser() error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{
			"email":         u.Email,
			"state":         u.State,
			"refresh_token": u.RefreshToken,
		},
	}

	_, err := collection.UpdateOne(
		ctx,
		bson.M{"email": u.Email},
		update,
		options.UpdateOne().SetUpsert(true),
	)

	return err
}

func GetAllUsers() ([]User, error) {
	var users []User

	if client == nil {
		return nil, fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cursor, err := collection.Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	if err = cursor.All(ctx, &users); err != nil {
		return nil, err
	}

	return users, nil
}

func GetRefreshToken(email string) (User, error) {
	var user User

	if client == nil {
		return user, fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		return user, err
	}
	return user, nil
}

// ------------------- TIMER FUNCTIONS -------------------

func SaveTimer(email string, timer Timing) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$push": bson.M{
			"timers": timer,
		},
	}

	_, err := collection.UpdateOne(ctx, bson.M{"email": email}, update)
	return err
}

// RemoveTimer now also removes associated jobs
func RemoveTimer(email string, timerID string) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}
	// First, remove the timer from the user's array
	collection := client.Database("afterwork").Collection("users")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	update := bson.M{"$pull": bson.M{"timers": bson.M{"id": timerID}}}
	_, err := collection.UpdateOne(ctx, bson.M{"email": email}, update)
	if err != nil {
		return fmt.Errorf("failed to remove timer from user: %w", err)
	}

	// Second, remove all pending jobs associated with this timer
	if err := RemoveJobsForTimer(timerID); err != nil {
		// Log this error but don't fail the whole operation,
		// as the user-facing timer is already gone.
		fmt.Printf("Warning: failed to remove pending jobs for timer %s: %v\n", timerID, err)
	}

	return nil
}

func GetTimers(email string) ([]Timing, error) {
	var user User
	if client == nil {
		return nil, fmt.Errorf("database not connected")
	}
	collection := client.Database("afterwork").Collection("users")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		return nil, err
	}
	return user.Timers, nil
}

// ------------------- JOB FUNCTIONS -------------------

// ScheduleJob adds a new job to the jobs collection.
func ScheduleJob(job *Job) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}
	collection := client.Database("afterwork").Collection("jobs")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := collection.InsertOne(ctx, job)
	return err
}

// GetPendingJobs retrieves all jobs with "PENDING" status.
func GetPendingJobs() ([]Job, error) {
	var jobs []Job
	if client == nil {
		return nil, fmt.Errorf("database not connected")
	}
	collection := client.Database("afterwork").Collection("jobs")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cursor, err := collection.Find(ctx, bson.M{"status": "PENDING"})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	if err = cursor.All(ctx, &jobs); err != nil {
		return nil, err
	}
	return jobs, nil
}

// CompleteJob marks a job's status as "COMPLETE".
func CompleteJob(jobID string) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}
	collection := client.Database("afterwork").Collection("jobs")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{"$set": bson.M{"status": "COMPLETE"}}
	_, err := collection.UpdateOne(ctx, bson.M{"_id": jobID}, update)
	return err
}

// RemoveJobsForTimer deletes all jobs associated with a given timerID.
func RemoveJobsForTimer(timerID string) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}
	collection := client.Database("afterwork").Collection("jobs")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := collection.DeleteMany(ctx, bson.M{"timer_id": timerID})
	return err
}
