package mongodb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/microservices-demo/user/users"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

var (
	TestMongo = Mongo{}
	// MONGO_TEST_HOST names a disposable MongoDB 3.6+, whose users database
	// the tests drop; tests that need one skip without it.
	TestHost = os.Getenv("MONGO_TEST_HOST")
	TestUser = users.User{
		FirstName: "firstname",
		LastName:  "lastname",
		Username:  "username",
		Password:  "blahblah",
		Addresses: []users.Address{
			users.Address{
				Street: "street",
			},
		},
	}
)

func TestMain(m *testing.M) {
	code := m.Run()
	if TestMongo.Client != nil {
		TestMongo.Client.Database(db).Drop(context.Background())
	}
	os.Exit(code)
}

// connect points the package at the test server, once.
func connect(t *testing.T) {
	if TestHost == "" {
		t.Skip("MONGO_TEST_HOST is not set")
	}
	if TestMongo.Client != nil {
		return
	}
	host = TestHost
	if err := TestMongo.Init(); err != nil {
		t.Fatal(err)
	}
}

func TestInit(t *testing.T) {
	saved := host
	defer func() { host = saved }()
	host = "localhost:1"
	if err := (&Mongo{}).Init(); err == nil {
		t.Error("expecting an unreachable server error")
	}
}

func TestNew(t *testing.T) {
	m := New()
	if m.AddressIDs == nil || m.CardIDs == nil {
		t.Error("Expected non nil arrays")
	}
}

func TestAddUserIDs(t *testing.T) {
	m := New()
	uid := primitive.NewObjectID()
	cid := primitive.NewObjectID()
	aid := primitive.NewObjectID()
	m.ID = uid
	m.AddressIDs = append(m.AddressIDs, aid)
	m.CardIDs = append(m.CardIDs, cid)
	m.AddUserIDs()
	if len(m.Addresses) != 1 && len(m.Cards) != 1 {
		t.Error(
			fmt.Sprintf(
				"Expected one card and one address added."))
	}
	if m.Addresses[0].ID != aid.Hex() {
		t.Error("Expected matching Address Hex")
	}
	if m.Cards[0].ID != cid.Hex() {
		t.Error("Expected matching Card Hex")
	}
	if m.UserID != uid.Hex() {
		t.Error("Expected matching User Hex")
	}
}

func TestAddressAddId(t *testing.T) {
	m := MongoAddress{Address: users.Address{}}
	id := primitive.NewObjectID()
	m.ID = id
	m.AddID()
	if m.Address.ID != id.Hex() {
		t.Error("Expected matching Address Hex")
	}
}

func TestCardAddId(t *testing.T) {
	m := MongoCard{Card: users.Card{}}
	id := primitive.NewObjectID()
	m.ID = id
	m.AddID()
	if m.Card.ID != id.Hex() {
		t.Error("Expected matching Card Hex")
	}
}

func TestCreate(t *testing.T) {
	connect(t)
	err := TestMongo.CreateUser(context.Background(), &TestUser)
	if err != nil {
		t.Error(err)
	}
	err = TestMongo.CreateUser(context.Background(), &TestUser)
	if err == nil {
		t.Error("Expected duplicate key error")
	}
}

func TestGetUserByName(t *testing.T) {
	connect(t)
	u, err := TestMongo.GetUserByName(context.Background(), TestUser.Username)
	if err != nil {
		t.Error(err)
	}
	if u.Username != TestUser.Username {
		t.Error("expected equal usernames")
	}
	_, err = TestMongo.GetUserByName(context.Background(), "bogususers")
	if err != ErrNotFound {
		t.Errorf("got %v, want mgo's not found error", err)
	}
}

func TestGetUser(t *testing.T) {
	connect(t)
	_, err := TestMongo.GetUser(context.Background(), TestUser.UserID)
	if err != nil {
		t.Error(err)
	}
}

func TestGetUserAttributes(t *testing.T) {
	connect(t)

}
func TestGetURL(t *testing.T) {
	name = "test"
	password = "password"
	host = "thishostshouldnotexist:3038"
	u := getURL()
	if u.String() != "mongodb://test:password@thishostshouldnotexist:3038/users" {
		t.Error("expected url mismatch")
	}
}

func TestPing(t *testing.T) {
	connect(t)
	err := TestMongo.Ping(context.Background())
	if err != nil {
		t.Error(err)
	}
}
