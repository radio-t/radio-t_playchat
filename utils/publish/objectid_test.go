package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestObjectIDJSON(t *testing.T) {
	// Zero value should marshal as "000000000000000000000000" (same as mongo-driver)
	zero := ObjectID{}
	b, err := json.Marshal(zero)
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if string(b) != `"000000000000000000000000"` {
		t.Errorf("zero ObjectID = %s, want \"000000000000000000000000\"", b)
	}

	// NewObjectID should produce 24-char hex
	id := NewObjectID()
	hex := id.Hex()
	if len(hex) != 24 {
		t.Errorf("Hex() length = %d, want 24", len(hex))
	}
	b, err = json.Marshal(id)
	if err != nil {
		t.Fatalf("marshal id: %v", err)
	}
	if string(b) != `"`+hex+`"` {
		t.Errorf("marshaled = %s, want %q", b, `"`+hex+`"`)
	}

	// Unmarshal back
	var id2 ObjectID
	err = json.Unmarshal(b, &id2)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if id2 != id {
		t.Errorf("round-trip failed: got %v, want %v", id2, id)
	}

	// Unmarshal empty string -> NilObjectID
	var id3 ObjectID
	err = json.Unmarshal([]byte(`""`), &id3)
	if err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if !id3.IsZero() {
		t.Errorf("empty string should decode to NilObjectID")
	}

	// Unmarshal extended JSON {"$oid": "..."}
	id4 := NewObjectID()
	extended := fmt.Sprintf(`{"$oid":"%s"}`, id4.Hex())
	var id5 ObjectID
	err = json.Unmarshal([]byte(extended), &id5)
	if err != nil {
		t.Fatalf("unmarshal extended: %v", err)
	}
	if id5 != id4 {
		t.Errorf("extended JSON round-trip failed: got %v, want %v", id5, id4)
	}

	// Unmarshal null -> no change
	var id6 ObjectID = NewObjectID()
	err = json.Unmarshal([]byte(`null`), &id6)
	if err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}

	// Uniqueness: multiple NewObjectID calls should produce different IDs
	ids := make(map[ObjectID]bool)
	for i := 0; i < 1000; i++ {
		id := NewObjectID()
		if ids[id] {
			t.Fatalf("duplicate ObjectID generated at iteration %d", i)
		}
		ids[id] = true
	}
}

func TestObjectIDStructOmitEmpty(t *testing.T) {
	// Verify omitempty behavior matches mongo-driver: zero ObjectID is NOT omitted
	type T struct {
		ID ObjectID `json:"id,omitempty"`
	}
	b, _ := json.Marshal(T{})
	if string(b) != `{"id":"000000000000000000000000"}` {
		t.Errorf("zero with omitempty = %s, want {\"id\":\"000000000000000000000000\"}", b)
	}
}