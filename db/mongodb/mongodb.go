package mongodb

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"time"

	"github.com/microservices-demo/user/users"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var (
	name     string
	password string
	host     string
	db       = "users"
	//ErrInvalidHexID represents a entity id that is not a valid bson ObjectID
	ErrInvalidHexID = errors.New("Invalid Id Hex")
)

func init() {
	flag.StringVar(&name, "mongo-user", os.Getenv("MONGO_USER"), "Mongo user")
	flag.StringVar(&password, "mongo-password", os.Getenv("MONGO_PASS"), "Mongo password")
	flag.StringVar(&host, "mongo-host", os.Getenv("MONGO_HOST"), "Mongo host")
}

// Mongo meets the Database interface requirements
type Mongo struct {
	//Client is a MongoDB client
	Client *mongo.Client
}

// Init MongoDB
func (m *Mongo) Init() error {
	u := getURL()
	opts := options.Client().ApplyURI(u.String()).
		SetConnectTimeout(5 * time.Second).
		SetServerSelectionTimeout(5 * time.Second).
		SetTimeout(10 * time.Second)
	client, err := mongo.Connect(context.Background(), opts)
	if err != nil {
		return err
	}
	// Connect does not dial; fail here, as mgo's Dial did, so main retries.
	if err := client.Ping(context.Background(), nil); err != nil {
		client.Disconnect(context.Background())
		return err
	}
	m.Client = client
	return m.EnsureIndexes()
}

func (m *Mongo) c(collection string) *mongo.Collection {
	return m.Client.Database(db).Collection(collection)
}

// MongoUser is a wrapper for the users
type MongoUser struct {
	users.User `bson:",inline"`
	ID         primitive.ObjectID   `bson:"_id"`
	AddressIDs []primitive.ObjectID `bson:"addresses"`
	CardIDs    []primitive.ObjectID `bson:"cards"`
}

// New Returns a new MongoUser
func New() MongoUser {
	u := users.New()
	return MongoUser{
		User:       u,
		AddressIDs: make([]primitive.ObjectID, 0),
		CardIDs:    make([]primitive.ObjectID, 0),
	}
}

// AddUserIDs adds userID as string to user
func (mu *MongoUser) AddUserIDs() {
	if mu.User.Addresses == nil {
		mu.User.Addresses = make([]users.Address, 0)
	}
	for _, id := range mu.AddressIDs {
		mu.User.Addresses = append(mu.User.Addresses, users.Address{
			ID: id.Hex(),
		})
	}
	if mu.User.Cards == nil {
		mu.User.Cards = make([]users.Card, 0)
	}
	for _, id := range mu.CardIDs {
		mu.User.Cards = append(mu.User.Cards, users.Card{ID: id.Hex()})
	}
	mu.User.UserID = mu.ID.Hex()
}

// MongoAddress is a wrapper for Address
type MongoAddress struct {
	users.Address `bson:",inline"`
	ID            primitive.ObjectID `bson:"_id"`
}

// AddID ObjectID as string
func (m *MongoAddress) AddID() {
	m.Address.ID = m.ID.Hex()
}

// MongoCard is a wrapper for Card
type MongoCard struct {
	users.Card `bson:",inline"`
	ID         primitive.ObjectID `bson:"_id"`
}

// AddID ObjectID as string
func (m *MongoCard) AddID() {
	m.Card.ID = m.ID.Hex()
}

func objectID(hex string) (primitive.ObjectID, bool) {
	id, err := primitive.ObjectIDFromHex(hex)
	return id, err == nil
}

func (m *Mongo) upsertID(collection string, id primitive.ObjectID, doc interface{}) error {
	_, err := m.c(collection).ReplaceOne(context.Background(), bson.M{"_id": id}, doc, options.Replace().SetUpsert(true))
	return err
}

// CreateUser Insert user to MongoDB, including connected addresses and cards, update passed in user with Ids
func (m *Mongo) CreateUser(u *users.User) error {
	id := primitive.NewObjectID()
	mu := New()
	mu.User = *u
	mu.ID = id
	var carderr error
	var addrerr error
	mu.CardIDs, carderr = m.createCards(u.Cards)
	mu.AddressIDs, addrerr = m.createAddresses(u.Addresses)
	err := m.upsertID("customers", mu.ID, mu)
	if err != nil {
		// Gonna clean up if we can, ignore error
		// because the user save error takes precedence.
		m.cleanAttributes(mu)
		return err
	}
	mu.User.UserID = mu.ID.Hex()
	// Cheap err for attributes
	if carderr != nil || addrerr != nil {
		return fmt.Errorf("%v %v", carderr, addrerr)
	}
	*u = mu.User
	return nil
}

func (m *Mongo) createCards(cs []users.Card) ([]primitive.ObjectID, error) {
	ids := make([]primitive.ObjectID, 0)
	for k, ca := range cs {
		id := primitive.NewObjectID()
		mc := MongoCard{Card: ca, ID: id}
		err := m.upsertID("cards", mc.ID, mc)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
		cs[k].ID = id.Hex()
	}
	return ids, nil
}

func (m *Mongo) createAddresses(as []users.Address) ([]primitive.ObjectID, error) {
	ids := make([]primitive.ObjectID, 0)
	for k, a := range as {
		id := primitive.NewObjectID()
		ma := MongoAddress{Address: a, ID: id}
		err := m.upsertID("addresses", ma.ID, ma)
		if err != nil {
			return ids, err
		}
		ids = append(ids, id)
		as[k].ID = id.Hex()
	}
	return ids, nil
}

func (m *Mongo) cleanAttributes(mu MongoUser) error {
	ctx := context.Background()
	_, err := m.c("addresses").DeleteMany(ctx, bson.M{"_id": bson.M{"$in": mu.AddressIDs}})
	_, err = m.c("cards").DeleteMany(ctx, bson.M{"_id": bson.M{"$in": mu.CardIDs}})
	return err
}

// updateUser updates one customer, failing as mgo's Update did when none matches.
func (m *Mongo) updateUser(userid string, update bson.M) error {
	uid, _ := objectID(userid)
	res, err := m.c("customers").UpdateOne(context.Background(), bson.M{"_id": uid}, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return mongo.ErrNoDocuments
	}
	return nil
}

func (m *Mongo) appendAttributeId(attr string, id primitive.ObjectID, userid string) error {
	return m.updateUser(userid, bson.M{"$addToSet": bson.M{attr: id}})
}

func (m *Mongo) removeAttributeId(attr string, id primitive.ObjectID, userid string) error {
	return m.updateUser(userid, bson.M{"$pull": bson.M{attr: id}})
}

// GetUserByName Get user by their name
func (m *Mongo) GetUserByName(name string) (users.User, error) {
	mu := New()
	err := m.c("customers").FindOne(context.Background(), bson.M{"username": name}).Decode(&mu)
	mu.AddUserIDs()
	return mu.User, err
}

// GetUser Get user by their object id
func (m *Mongo) GetUser(id string) (users.User, error) {
	uid, ok := objectID(id)
	if !ok {
		return users.New(), errors.New("Invalid Id Hex")
	}
	mu := New()
	err := m.c("customers").FindOne(context.Background(), bson.M{"_id": uid}).Decode(&mu)
	mu.AddUserIDs()
	return mu.User, err
}

func (m *Mongo) findAll(collection string, filter bson.M, out interface{}) error {
	ctx := context.Background()
	cursor, err := m.c(collection).Find(ctx, filter)
	if err != nil {
		return err
	}
	return cursor.All(ctx, out)
}

// GetUsers Get all users
func (m *Mongo) GetUsers() ([]users.User, error) {
	// TODO: add paginations
	var mus []MongoUser
	err := m.findAll("customers", bson.M{}, &mus)
	us := make([]users.User, 0)
	for _, mu := range mus {
		mu.AddUserIDs()
		us = append(us, mu.User)
	}
	return us, err
}

// GetUserAttributes given a user, load all cards and addresses connected to that user
func (m *Mongo) GetUserAttributes(u *users.User) error {
	ids := make([]primitive.ObjectID, 0)
	for _, a := range u.Addresses {
		id, ok := objectID(a.ID)
		if !ok {
			return ErrInvalidHexID
		}
		ids = append(ids, id)
	}
	var ma []MongoAddress
	err := m.findAll("addresses", bson.M{"_id": bson.M{"$in": ids}}, &ma)
	if err != nil {
		return err
	}
	na := make([]users.Address, 0)
	for _, a := range ma {
		a.Address.ID = a.ID.Hex()
		na = append(na, a.Address)
	}
	u.Addresses = na

	ids = make([]primitive.ObjectID, 0)
	for _, c := range u.Cards {
		id, ok := objectID(c.ID)
		if !ok {
			return ErrInvalidHexID
		}
		ids = append(ids, id)
	}
	var mc []MongoCard
	err = m.findAll("cards", bson.M{"_id": bson.M{"$in": ids}}, &mc)
	if err != nil {
		return err
	}

	nc := make([]users.Card, 0)
	for _, ca := range mc {
		ca.Card.ID = ca.ID.Hex()
		nc = append(nc, ca.Card)
	}
	u.Cards = nc
	return nil
}

// GetCard Gets card by objects Id
func (m *Mongo) GetCard(id string) (users.Card, error) {
	cid, ok := objectID(id)
	if !ok {
		return users.Card{}, errors.New("Invalid Id Hex")
	}
	mc := MongoCard{}
	err := m.c("cards").FindOne(context.Background(), bson.M{"_id": cid}).Decode(&mc)
	mc.AddID()
	return mc.Card, err
}

// GetCards Gets all cards
func (m *Mongo) GetCards() ([]users.Card, error) {
	// TODO: add pagination
	var mcs []MongoCard
	err := m.findAll("cards", bson.M{}, &mcs)
	cs := make([]users.Card, 0)
	for _, mc := range mcs {
		mc.AddID()
		cs = append(cs, mc.Card)
	}
	return cs, err
}

// CreateCard adds card to MongoDB
func (m *Mongo) CreateCard(ca *users.Card, userid string) error {
	if _, ok := objectID(userid); userid != "" && !ok {
		return errors.New("Invalid Id Hex")
	}
	id := primitive.NewObjectID()
	mc := MongoCard{Card: *ca, ID: id}
	err := m.upsertID("cards", mc.ID, mc)
	if err != nil {
		return err
	}
	// Address for anonymous user
	if userid != "" {
		err = m.appendAttributeId("cards", mc.ID, userid)
		if err != nil {
			return err
		}
	}
	mc.AddID()
	*ca = mc.Card
	return err
}

// GetAddress Gets an address by object Id
func (m *Mongo) GetAddress(id string) (users.Address, error) {
	aid, ok := objectID(id)
	if !ok {
		return users.Address{}, errors.New("Invalid Id Hex")
	}
	ma := MongoAddress{}
	err := m.c("addresses").FindOne(context.Background(), bson.M{"_id": aid}).Decode(&ma)
	ma.AddID()
	return ma.Address, err
}

// GetAddresses gets all addresses
func (m *Mongo) GetAddresses() ([]users.Address, error) {
	// TODO: add pagination
	var mas []MongoAddress
	err := m.findAll("addresses", bson.M{}, &mas)
	as := make([]users.Address, 0)
	for _, ma := range mas {
		ma.AddID()
		as = append(as, ma.Address)
	}
	return as, err
}

// CreateAddress Inserts Address into MongoDB
func (m *Mongo) CreateAddress(a *users.Address, userid string) error {
	if _, ok := objectID(userid); userid != "" && !ok {
		return errors.New("Invalid Id Hex")
	}
	id := primitive.NewObjectID()
	ma := MongoAddress{Address: *a, ID: id}
	err := m.upsertID("addresses", ma.ID, ma)
	if err != nil {
		return err
	}
	// Address for anonymous user
	if userid != "" {
		err = m.appendAttributeId("addresses", ma.ID, userid)
		if err != nil {
			return err
		}
	}
	ma.AddID()
	*a = ma.Address
	return err
}

// CreateAddress Inserts Address into MongoDB
func (m *Mongo) Delete(entity, id string) error {
	oid, ok := objectID(id)
	if !ok {
		return errors.New("Invalid Id Hex")
	}
	ctx := context.Background()
	if entity == "customers" {
		u, err := m.GetUser(id)
		if err != nil {
			return err
		}
		aids := make([]primitive.ObjectID, 0)
		for _, a := range u.Addresses {
			aid, _ := objectID(a.ID)
			aids = append(aids, aid)
		}
		cids := make([]primitive.ObjectID, 0)
		for _, c := range u.Cards {
			cid, _ := objectID(c.ID)
			cids = append(cids, cid)
		}
		m.c("addresses").DeleteMany(ctx, bson.M{"_id": bson.M{"$in": aids}})
		m.c("cards").DeleteMany(ctx, bson.M{"_id": bson.M{"$in": cids}})
	} else {
		m.c("customers").UpdateMany(ctx, bson.M{},
			bson.M{"$pull": bson.M{entity: oid}})
	}
	res, err := m.c(entity).DeleteOne(ctx, bson.M{"_id": oid})
	if err == nil && res.DeletedCount == 0 {
		// mgo's Remove failed when nothing matched.
		err = mongo.ErrNoDocuments
	}
	return err
}

func getURL() url.URL {
	ur := url.URL{
		Scheme: "mongodb",
		Host:   host,
		Path:   db,
	}
	if name != "" {
		u := url.UserPassword(name, password)
		ur.User = u
	}
	return ur
}

// EnsureIndexes ensures username is unique
func (m *Mongo) EnsureIndexes() error {
	i := mongo.IndexModel{
		Keys:    bson.D{{Key: "username", Value: 1}},
		Options: options.Index().SetUnique(true).SetBackground(true),
	}
	_, err := m.c("customers").Indexes().CreateOne(context.Background(), i)
	return err
}

func (m *Mongo) Ping() error {
	return m.Client.Ping(context.Background(), nil)
}
