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

type User struct {
	Email        string `json:"email" bson:"email"`
	State        string `json:"state" bson:"state"`
	RefreshToken string `json:"refresh_token" bson:"refresh_token"`
	Timer        *Timer `json:"timer,omitempty" bson:"timer,omitempty"`
}

type Timer struct {
	StartTime string `json:"start_time" bson:"start_time"`
	Duration  int    `json:"duration" bson:"duration"`
	IsDaily   bool   `json:"is_daily" bson:"is_daily"`
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

	filter := bson.M{"email": u.Email}
	update := bson.M{"$set": u}

	_, err := collection.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
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

func SaveTimer(email string, timer Timer) error {
	if client == nil {
		return fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	update := bson.M{
		"$set": bson.M{
			"timer": timer,
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

func GetTimer(email string) (*Timer, error) {
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

	return user.Timer, nil
}
