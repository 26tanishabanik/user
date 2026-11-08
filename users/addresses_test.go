package users

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAddLinksAdd(t *testing.T) {
	domain = "mydomain"
	a := Address{ID: "test"}
	a.AddLinks()
	h := Href{"http://mydomain/addresses/test"}
	if !reflect.DeepEqual(a.Links["address"], h) {
		t.Error("expected equal address links")
	}

}

func TestLinksEncodeHref(t *testing.T) {
	l := Links{"self": Href{"http://user/addresses/test"}}
	b, err := json.Marshal(l)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"self":{"href":"http://user/addresses/test"}}` {
		t.Errorf("links encoded as %s", b)
	}
}
