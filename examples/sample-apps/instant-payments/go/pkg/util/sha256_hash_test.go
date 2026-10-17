package util

import (
	"testing"
	"time"
)

type hashFixture struct {
	Name      string
	Count     int
	Amount    float64
	When      time.Time
	Payload   []byte
	IgnoredID string `hash:"-"`
}

func TestSha256HashDeterministicAndHonorsIgnoredFields(t *testing.T) {
	fixture := hashFixture{Name: "payment", Count: 2, Amount: 10.5, When: time.Unix(100, 0), Payload: []byte("payload"), IgnoredID: "first"}
	first, err := Sha256Hash(fixture)
	if err != nil {
		t.Fatal(err)
	}
	fixture.IgnoredID = "second"
	second, err := Sha256Hash(&fixture)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected ignored field not to affect hash: %s != %s", first, second)
	}
	fixture.Name = "different"
	third, err := Sha256Hash(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("expected changed field to change hash")
	}
}

func TestSha256HashRejectsUnsupportedValues(t *testing.T) {
	for _, value := range []any{nil, "string", []string{"unsupported"}, struct{ Flag bool }{Flag: true}} {
		if _, err := Sha256Hash(value); err == nil {
			t.Fatalf("expected error for %#v", value)
		}
	}
	var nilFixture *hashFixture
	if _, err := Sha256Hash(nilFixture); err == nil {
		t.Fatal("expected nil pointer error")
	}
}
