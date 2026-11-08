package users

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

var (
	domain    string
	entitymap = map[string]string{
		"customer": "customers",
		"address":  "addresses",
		"card":     "cards",
	}
)

func init() {
	flag.StringVar(&domain, "link-domain", os.Getenv("HATEAOS"), "HATEAOS link domain")
}

type Links map[string]Href

func (l *Links) AddLink(ent string, id string) {
	nl := make(Links)
	link := fmt.Sprintf("http://%v/%v/%v", domain, entitymap[ent], id)
	nl[ent] = Href{link}
	nl["self"] = Href{link}
	*l = nl

}

func (l *Links) AddAttrLink(attr string, corent string, id string) {
	link := fmt.Sprintf("http://%v/%v/%v/%v", domain, entitymap[corent], id, entitymap[attr])
	nl := *l
	nl[entitymap[attr]] = Href{link}
	*l = nl
}

func (l *Links) AddCustomer(id string) {
	l.AddLink("customer", id)
	l.AddAttrLink("address", "customer", id)
	l.AddAttrLink("card", "customer", id)
}

func (l *Links) AddAddress(id string) {
	l.AddLink("address", id)
}

func (l *Links) AddCard(id string) {
	l.AddLink("card", id)
}

type Href struct {
	string `json:"href"`
}

// MarshalJSON writes the href explicitly: Go before 1.10 encoded the embedded
// unexported field, later releases skip it and would send {}.
func (h Href) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Href string `json:"href"`
	}{h.string})
}
