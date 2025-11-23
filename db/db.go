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

type User struct {
	Email        string `json:"email"`
	RefreshToken string `json:"refresh_token"`
}

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
	fmt.Println("Pinged your deployment. Connected to MongoDB!")
}

func Disconnect() {
	if err := client.Disconnect(context.TODO()); err != nil {
		panic(err)
	}
}

// ---------------------------------------------------------------------

// Add or update user refresh token
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

// Get stored refresh token by email
func GetRefreshToken(email string) (string, error) {
	if client == nil {
		return "", fmt.Errorf("database not connected")
	}

	collection := client.Database("afterwork").Collection("users")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user User
	err := collection.FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return "", nil // email not registered yet
		}
		return "", err
	}

	return user.RefreshToken, nil
}
