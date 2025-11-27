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

type Timing struct {
	StartTime string `json:"starttime"`
	Duration  int    `json:"duration"`
	IsDaily   bool   `json:"isdaily"`
}

type User struct {
	Email        string `json:"email"`
	State        string `json:"state"`
	RefreshToken string `json:"refresh_token"`
	Timer        Timing `json:"timer"`
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

	// Only update timer if provided
	update["$set"].(bson.M)["timer"] = bson.M{
		"starttime": u.Timer.StartTime,
		"duration":  u.Timer.Duration,
		"isdaily":   u.Timer.IsDaily,
	}

	_, err := collection.UpdateOne(
		ctx,
		bson.M{"email": u.Email},
		update,
		options.UpdateOne().SetUpsert(true),
	)

	return err
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
		if err == mongo.ErrNoDocuments {
			return user, fmt.Errorf("email not registered")
		}
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
		"$set": bson.M{
			"timer": bson.M{
				"starttime": timer.StartTime,
				"duration":  timer.Duration,
				"isdaily":   timer.IsDaily,
			},
		},
	}

	_, err := collection.UpdateOne(ctx, bson.M{"email": email}, update)
	return err
}

func RemoveTimer(email string) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$unset": bson.M{
			"timer": "",
		},
	}

	_, err := collection.UpdateOne(ctx, bson.M{"email": email}, update)
	return err
}

func GetTimer(email string) (Timing, error) {
	var user User

	if client == nil {
		return user.Timer, fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		return user.Timer, err
	}

	return user.Timer, nil
}
